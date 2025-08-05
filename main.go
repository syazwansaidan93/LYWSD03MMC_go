package main

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-ble/ble"
	"github.com/go-ble/ble/linux" // Linux-specific adapter
	_ "github.com/mattn/go-sqlite3" // SQLite driver
)

// Config represents the structure of config.json
type Config struct {
	MACAddresses      []string `json:"mac_addresses"`
	PollIntervalMinutes int    `json:"poll_interval_minutes"`
}

const (
	configFile                 = "config.json"
	databaseName               = "sensor_data.db"
	dataRetentionDays          = 1 // Changed from 7 to 1 day
	connectionTimeoutSeconds   = 20 * time.Second
	dataCharacteristicUUID     = "ebe0ccc1-7a0a-4b0c-8a1a-6ff2997da3a6"
	scanTimeoutSeconds         = 20 * time.Second // Increased timeout for scanning
	notificationWaitTimeoutSeconds = 30 * time.Second // Increased timeout for notifications
	maxCollectionRetries       = 3
	retryDelaySeconds          = 5 * time.Second
)

var (
	macToMonitor string
	pollInterval time.Duration
	dbPath       string
	// lastSavedTime is not strictly needed as a global for the Go version
	// but kept for conceptual parity, though its usage is localized.
	lastSavedTime time.Time
)

func init() {
	// Configure logging to stdout
	log.SetOutput(os.Stdout)
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	// Determine script directory for config and database paths
	// The user specified /var/www/sensor-data as the new database location.
	dbDir := "/var/www/sensor-data"
	dbPath = filepath.Join(dbDir, databaseName)

	// Ensure the database directory exists
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		log.Fatalf("Failed to create database directory %s: %v", dbDir, err)
	}


	ex, err := os.Executable()
	if err != nil {
		log.Fatalf("Failed to get executable path: %v", err)
	}
	scriptDir := filepath.Dir(ex)
	configPath := filepath.Join(scriptDir, configFile)


	// Load configuration
	configData, err := loadConfig(configPath)
	if err != nil {
		log.Fatalf("Error loading configuration: %v", err)
	}

	if len(configData.MACAddresses) == 0 {
		log.Fatal("Error: No MAC addresses found in config.json.")
	}
	if len(configData.MACAddresses) > 1 {
		log.Printf("Warning: config.json contains more than one MAC address. This script is optimized for a single sensor and will only process the first one found.")
	}

	macToMonitor = strings.ToUpper(configData.MACAddresses[0])
	pollInterval = time.Duration(configData.PollIntervalMinutes) * time.Minute

	lastSavedTime = time.Time{} // Initialize to zero time
}

// loadConfig reads and parses the config.json file.
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("file not found: %s. Please create it with 'mac_addresses'. Error: %w", path, err)
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("could not parse %s. Check JSON format. Error: %w", path, err)
	}
	return &config, nil
}

// getDBConnection establishes a connection to the SQLite database.
func getDBConnection() (*sql.DB, error) {
	conn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	return conn, nil
}

// setupDatabase creates the sensor_readings table if it doesn't exist.
func setupDatabase() {
	conn, err := getDBConnection()
	if err != nil {
		log.Fatalf("Failed to get DB connection for setup: %v", err)
	}
	defer conn.Close()

	createTableSQL := `
	CREATE TABLE IF NOT EXISTS sensor_readings (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp DATETIME NOT NULL,
		temperature REAL,
		humidity INTEGER
	);`

	createIndexSQL := `
	CREATE INDEX IF NOT EXISTS idx_timestamp ON sensor_readings (timestamp);`

	_, err = conn.Exec(createTableSQL)
	if err != nil {
		log.Fatalf("Failed to create table: %v", err)
	}

	_, err = conn.Exec(createIndexSQL)
	if err != nil {
		log.Fatalf("Failed to create index: %v", err)
	}
	log.Println("Database setup complete.")
}

// storeSensorData inserts temperature and humidity readings into the database.
func storeSensorData(temperature float64, humidity int) {
	conn, err := getDBConnection()
	if err != nil {
		log.Printf("Error getting DB connection for storing data: %v", err)
		return
	}
	defer conn.Close()

	currentTime := time.Now()
	_, err = conn.Exec(`
		INSERT INTO sensor_readings (timestamp, temperature, humidity)
		VALUES (?, ?, ?)
	`, currentTime.Format("2006-01-02 15:04:05.000"), temperature, humidity) // SQLite stores DATETIME as text

	if err != nil {
		log.Printf("Error storing data: %v", err)
	} else {
		lastSavedTime = currentTime
		log.Printf("Saved data: T=%.2f°C, H=%d%% at %s.", temperature, humidity, currentTime.Format("2006-01-02 15:04:05"))
	}
}

// applyRetentionPolicy deletes old records from the database.
func applyRetentionPolicy() {
	conn, err := getDBConnection()
	if err != nil {
		log.Printf("Error getting DB connection for retention policy: %v", err)
		return
	}
	defer conn.Close()

	thresholdTime := time.Now().Add(-time.Duration(dataRetentionDays) * 24 * time.Hour)
	result, err := conn.Exec(`DELETE FROM sensor_readings WHERE timestamp < ?`, thresholdTime.Format("2006-01-02 15:04:05.000"))
	if err != nil {
		log.Printf("Error applying retention policy: %v", err)
		return
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		log.Printf("Error getting rows affected by retention policy: %v", err)
		return
	}

	if rowsAffected > 0 {
		log.Printf("Applied retention policy: Deleted %d records older than %d days.", rowsAffected, dataRetentionDays)
	} else {
		log.Printf("Retention policy ran: No data older than %d days to delete.", rowsAffected, dataRetentionDays)
	}
}

// collectSingleReading attempts to connect to the BLE device and collect a single reading.
func collectSingleReading(macAddress string) (temperature float64, humidity int, err error) {
	log.Printf("Attempting to connect to %s...", macAddress)

	clientCtx, clientCancel := context.WithTimeout(context.Background(), connectionTimeoutSeconds)
	defer clientCancel()

	// Let ble.Connect handle the scanning and connection in one go.
	// It will scan until it finds a device matching this filter and then connect.
	cln, err := ble.Connect(clientCtx, func(a ble.Advertisement) bool {
		// This filter will be used by ble.Connect to find the device.
		// It will continue scanning until this function returns true for a device.
		if strings.ToUpper(a.Addr().String()) == macAddress {
			log.Printf("Device %s found during connection scan: %s", macAddress, a.LocalName())
			return true
		}
		return false
	})
	if err != nil {
		return 0, 0, fmt.Errorf("failed to connect to %s: %w", macAddress, err)
	}
	defer cln.CancelConnection() // Ensure disconnection on exit

	log.Printf("Connected to %s. Discovering services...", macAddress)

	// Discover all services
	p, err := cln.DiscoverProfile(true)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to discover profile: %w", err)
	}

	var dataChar *ble.Characteristic
	for _, s := range p.Services {
		for _, c := range s.Characteristics {
			if c.UUID.Equal(ble.MustParse(dataCharacteristicUUID)) {
				dataChar = c
				break
			}
		}
		if dataChar != nil {
			break
		}
	}

	if dataChar == nil {
		return 0, 0, fmt.Errorf("data characteristic %s not found", dataCharacteristicUUID)
	}

	// New approach: The notification handler will store the data in a variable and signal a channel.
	var collectedTemp float64
	var collectedHumid int
	var dataReady = make(chan struct{}) // Signal channel

	notificationHandler := func(data []byte) {
		log.Printf("Raw notification data received: %x (length: %d)", data, len(data)) // Log raw data
		if len(data) >= 3 {
			tempBytes := data[0:2]
			collectedTemp = float64(int16(binary.LittleEndian.Uint16(tempBytes))) / 100.0
			collectedHumid = int(data[2])
			close(dataReady) // Signal that data is ready
			log.Printf("Notification received and parsed: T=%.2f, H=%d", collectedTemp, collectedHumid)
		} else {
			log.Printf("Received malformed notification data (less than 3 bytes): %v", data)
		}
	}

	// Subscribe with the corrected handler
	err = cln.Subscribe(dataChar, true, notificationHandler)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to subscribe to characteristic %s: %w", dataCharacteristicUUID, err)
	}

	// --- NEW ADDITION: Try to read the characteristic once after subscribing ---
	// This might be necessary for some sensors to "kickstart" notifications
	// or to get an immediate value if the sensor doesn't notify immediately upon subscribe.
	readVal, readErr := cln.ReadCharacteristic(dataChar)
	if readErr == nil {
		log.Printf("Successfully read characteristic %s: %x (length: %d)", dataCharacteristicUUID, readVal, len(readVal))
		if len(readVal) >= 3 {
			tempBytes := readVal[0:2]
			readTemp := float64(int16(binary.LittleEndian.Uint16(tempBytes))) / 100.0
			readHumid := int(readVal[2])
			// If we get a valid read, use it as the collected data for this attempt
			collectedTemp = readTemp
			collectedHumid = readHumid
			close(dataReady) // Signal that data is ready from the read
			log.Printf("Read and parsed: T=%.2f, H=%d", collectedTemp, collectedHumid)
		} else {
			log.Printf("Read malformed data (less than 3 bytes): %v", readVal)
		}
	} else {
		log.Printf("Failed to read characteristic %s: %v", dataCharacteristicUUID, readErr)
	}
	// --- END NEW ADDITION ---

	select {
	case <-dataReady:
		log.Printf("Successfully received data: T=%.2f°C, H=%d%% from %s", collectedTemp, collectedHumid, macAddress)
		return collectedTemp, collectedHumid, nil
	case <-time.After(notificationWaitTimeoutSeconds):
		log.Printf("Timeout waiting for data notification from %s after %s.", macAddress, notificationWaitTimeoutSeconds)
		return 0, 0, fmt.Errorf("notification timeout")
	}
}

// collectorLoop periodically collects sensor data.
func collectorLoop(wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		var temperature float64
		var humidity int
		var collectionErr error

		for attempt := 1; attempt <= maxCollectionRetries; attempt++ {
			log.Printf("Collection attempt %d/%d for %s...", attempt, maxCollectionRetries, macToMonitor)
			temperature, humidity, collectionErr = collectSingleReading(macToMonitor)

			if collectionErr == nil {
				storeSensorData(temperature, humidity)
				break // Success, break retry loop
			} else {
				log.Printf("Collection failed for %s on attempt %d: %v. Retrying in %s...", macToMonitor, attempt, collectionErr, retryDelaySeconds)
				time.Sleep(retryDelaySeconds)
			}
		}

		if collectionErr != nil {
			log.Printf("Failed to collect data from %s after %d attempts. Will try again in the next interval.", macToMonitor, maxCollectionRetries)
		}

		log.Printf("Waiting for %s until next scheduled collection...", pollInterval)
		time.Sleep(pollInterval)
	}
}

// retentionLoop periodically applies the data retention policy.
func retentionLoop(wg *sync.WaitGroup) {
	defer wg.Done()
	// Run once immediately, then every 24 hours
	applyRetentionPolicy()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		log.Println("Running daily data retention policy...")
		applyRetentionPolicy()
	}
}

func main() {
	log.Println("Starting sensor data collector script...")

	// Initialize BLE adapter once
	d, err := linux.NewDevice()
	if err != nil {
		log.Fatalf("Can't new device: %v", err)
	}
	ble.SetDefaultDevice(d)
	defer d.Stop() // Ensure the device is stopped on application exit

	setupDatabase()

	var wg sync.WaitGroup
	wg.Add(2) // Two goroutines: collectorLoop and retentionLoop

	go collectorLoop(&wg)
	go retentionLoop(&wg)

	// Keep the main goroutine alive until interrupted
	// In a real application, you might use a context for graceful shutdown.
	// For this script, we'll wait indefinitely.
	select {} // Block forever, or until process is killed (e.g., Ctrl+C)
}

