# Go BLE Sensor LYWSD03MMC Data Collector

This project provides a robust solution for collecting temperature and humidity data from a specific Bluetooth Low Energy (BLE) sensor (e.g., Xiaomi LYWSD03MMC) using a Go application running on an Orange Pi Zero 3. The collected data is stored in an SQLite database, and a PHP API is provided to easily retrieve this data for visualization or other applications.

## Features

* **BLE Data Collection:** Connects to a specified BLE sensor and collects temperature and humidity readings.

* **Persistent Storage:** Stores collected data in an SQLite database.

* **Data Retention:** Automatically prunes old data, keeping only the last 1 day of readings.

* **Reliable Operation:** Designed to run as a systemd service, ensuring automatic startup on boot and crash recovery.

* **HTTP API:** Provides endpoints to fetch the latest sensor reading, historical data, and daily summaries via a simple PHP interface.

## Prerequisites

To set up and run this project on your **Orange Pi Zero Zero 3** (or similar Linux-based SBC), you will need:

* **Go (Golang)**: Version 1.18 or higher.

* **BlueZ**: The Linux Bluetooth protocol stack. This is usually pre-installed on most Linux distributions.

* **PHP**: With `php-fpm` (for Nginx) or `mod_php` (for Apache) and `php-sqlite3` extension.

* **Web Server**: Nginx or Apache to serve the PHP API.

* **SQLite3**: The database engine (usually pre-installed or easily installed via `apt`).

* **BLE Sensor**: A compatible temperature/humidity sensor (e.g., Xiaomi LYWSD03MMC) with the characteristic UUID `ebe0ccc1-7a0a-4b0c-8a1a-6ff2997da3a6` for data.

## Setup

### 1. Go Application Setup

The Go application is responsible for connecting to your BLE sensor and storing data in the database.

**a. Clone the Repository (or place files):**

Place the `main.go` file (your Go sensor collector code) in your desired working directory, for example, `/home/wan/sensor/`.

```bash
mkdir -p /home/wan/sensor
# Copy your main.go file here
```

**b. Create `config.json`:**

In the `/home/wan/sensor/` directory, create a `config.json` file. Replace `XX:XX:XX:XX:XX:XX` with the actual MAC address of your BLE sensor.

```json
{
    "mac_addresses": ["A4:C1:38:E6:AD:AD"],
    "poll_interval_minutes": 15
}
```

**c. Initialize Go Module and Download Dependencies:**

Navigate to your project directory and download the necessary Go modules.

```bash
cd /home/wan/sensor
go mod init sensor_collector # You can choose a different module name
go get github.com/go-ble/ble
go get github.com/go-ble/ble/linux
go get github.com/mattn/go-sqlite3
```

**d. Build the Executable:**

Compile your Go application. This will create an executable named `sensor_collector` in the current directory.

```bash
go build -o sensor_collector
```

### 2. PHP API Setup

The PHP API will read data from the SQLite database and serve it via HTTP.

**a. Place `index.php`:**

Copy your `index.php` (or `sensor_api.php`) file to your web server's document root. A common location for Nginx is `/var/www/html/`.

```bash
sudo cp /path/to/your/index.php /var/www/html/
```

**Note:** Ensure the `DATABASE_PATH` constant in your PHP file is set correctly:
`define('DATABASE_PATH', '/var/www/sensor-data/sensor_data.db');`

**b. Create Database Directory and Set Permissions:**

The Go application will create the SQLite database at `/var/www/sensor-data/sensor_data.db`. Ensure this directory exists and that your web server user (e.g., `www-data` for Nginx/Apache) has read permissions to the database file once it's created by the Go application.

```bash
sudo mkdir -p /var/www/sensor-data
sudo chown wan:wan /var/www/sensor-data # Ensure 'wan' user can write to it
sudo chmod 755 /var/www/sensor-data
```

The Go application (running as root via systemd) will create the database. Once created, you might need to adjust permissions for the web server to read it:

```bash
# After the Go app has run at least once and created the db file:
sudo chown www-data:www-data /var/www/sensor-data/sensor_data.db # Or adjust to your web server user
sudo chmod 644 /var/www/sensor-data/sensor_data.db
```

**c. Configure Web Server (Example for Nginx):**

If you're using Nginx, you'll need to configure it to process PHP files. Here's a basic example of a server block (usually in `/etc/nginx/sites-available/default` or a new file in `sites-available` and symlinked to `sites-enabled`):

```nginx
server {
    listen 80;
    server_name ; # Replace with your Orange Pi's IP or domain

    root /var/www/html/sensor;

    index index.html index.php;

    location / {
        try_files $uri $uri/ /index.php?$query_string;
    }

    location ~ \.php$ {
        root /var/www/html/sensor;
        try_files $uri =404;
        fastcgi_pass unix:/run/php/php8.2-fpm.sock; # VERIFY THIS PATH
        fastcgi_index index.php;
        fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;
        include fastcgi_params;
    }
}
```

Remember to restart Nginx after configuration changes: `sudo systemctl restart nginx`.

### 3. Systemd Service Setup

To ensure your Go application runs continuously and automatically, set it up as a systemd service.

**a. Create the Service File:**

Create a new service file named `sensor-collector.service` in `/etc/systemd/system/`:

```bash
sudo nano /etc/systemd/system/sensor-collector.service
```

Paste the following content into the file:

```ini
[Unit]
Description=Go BLE Sensor Data Collector
After=network.target

[Service]
Type=simple
Restart=on-failure
RestartSec=5s
ExecStart=/home/wan/sensor/sensor_collector
WorkingDirectory=/home/wan/sensor
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

**b. Reload Systemd Daemon:**

```bash
sudo systemctl daemon-reload
```

**c. Enable the Service:**

```bash
sudo systemctl enable sensor-collector.service
```

**d. Start the Service:**

```bash
sudo systemctl start sensor-collector.service
```

**e. Check Service Status and Logs:**

```bash
sudo systemctl status sensor-collector.service
sudo journalctl -u sensor-collector.service -f
```

## Usage (API Endpoints)

Once the PHP API is set up and the Go collector is running, you can access the data via HTTP requests. Replace `your_orange_pi_ip` with the actual IP address or domain name of your Orange Pi Zero 3.

* **Get Latest Reading:**
  `GET http://your_orange_pi_ip/api/latest`
  Example Response:

  ```json
  {
      "time": "20:00",
      "temp": "25.50",
      "humid": "60"
  }
  ```

* **Get Full History:**
  `GET http://your_orange_pi_ip/api/history`
  (Optional parameters: `?limit=N` for N latest records, `?order=asc` for ascending timestamp order)
  Example Response:

  ```json
  [
      {
          "time": "18:00",
          "temp": "24.80",
          "humid": "58",
          "date": "2025-08-05"
      },
      {
          "time": "20:00",
          "temp": "25.50",
          "humid": "60",
          "date": "2025-08-05"
      }
  ]
  ```

* **Get Daily History for a Specific Date:**
  `GET http://your_orange_pi_ip/api/daily_history/YYYY-MM-DD`
  Example: `http://your_orange_pi_ip/api/daily_history/2025-08-05`
  Example Response:

  ```json
  [
      {
          "time": "18:00",
          "temp": "24.80",
          "humid": "58",
          "date": "2025-08-05"
      },
      {
          "time": "20:00",
          "temp": "25.50",
          "humid": "60",
          "date": "2025-08-05"
      }
  ]
  ```

## Configuration

The `config.json` file controls the Go application's behavior:

* `mac_addresses`: An array containing the MAC address(es) of your BLE sensor(s). The script currently processes only the first address in the list.

* `poll_interval_minutes`: The interval (in minutes) at which the sensor data will be collected.

## Database

The sensor data is stored in an SQLite database located at `/var/www/sensor-data/sensor_data.db`.
The database schema is:

```sql
CREATE TABLE sensor_readings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp DATETIME NOT NULL,
    temperature REAL,
    humidity INTEGER
);
CREATE INDEX idx_timestamp ON sensor_readings (timestamp);
```

Data older than 1 day is automatically removed by the Go application's retention policy.

## Troubleshooting

* **"device or resource busy" error for `hci0`:**
  This means another process is using the Bluetooth adapter. Stop the system's Bluetooth service before starting your collector: `sudo systemctl stop bluetooth`. The systemd service file provided handles this automatically.

* **"notification timeout" / No data received:**

  * Ensure your BLE sensor is powered on and within range of the Orange Pi.

  * Verify the `DATA_CHARACTERISTIC_UUID` in `main.go` is correct for your sensor.

  * Check the sensor's battery level.

* **PHP API "Could not connect to the database." error:**

  * Ensure the `DATABASE_PATH` in `index.php` is correct (`/var/www/sensor-data/sensor_data.db`).

  * Verify that the `/var/www/sensor-data` directory exists and that the web server user (`www-data` or similar) has read permissions to the `sensor_data.db` file.

  * Check your web server (Nginx/Apache) and PHP-FPM error logs for more details.

* **Go application not starting or logging:**

  * Check `sudo systemctl status sensor-collector.service` for service status.

  * Use `sudo journalctl -u sensor-collector.service -f` to view live logs for errors.

  * Ensure the `sensor_collector` executable has correct permissions (`sudo chmod +x /home/wan/sensor/sensor_collector`).
