<?php
// index.php (or sensor_api.php if you prefer that name and adjust Nginx)

// --- Configuration Constants ---
// Changed to the absolute path where the GoLang sensor_collector writes the database
define('DATABASE_PATH', '/var/www/sensor-data/sensor_data.db'); 

// --- CORS Headers ---
// Allow requests from any origin (for development).
// In production, you might want to restrict this to your frontend's domain.
header("Access-Control-Allow-Origin: *");
header("Access-Control-Allow-Methods: GET, POST, OPTIONS");
header("Access-Control-Allow-Headers: Content-Type, Authorization, X-Requested-With");
header("Content-Type: application/json");

// Handle OPTIONS requests for CORS preflight
if ($_SERVER['REQUEST_METHOD'] === 'OPTIONS') {
    http_response_code(200);
    exit();
}

// --- Database Functions ---
function getDbConnection() {
    // Use the defined absolute database path
    $dbPath = DATABASE_PATH;
    try {
        $pdo = new PDO("sqlite:" . $dbPath);
        $pdo->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION); // Throw exceptions on errors
        $pdo->setAttribute(PDO::ATTR_DEFAULT_FETCH_MODE, PDO::FETCH_ASSOC); // Fetch rows as associative arrays
        return $pdo;
    } catch (PDOException $e) {
        // Log the error for debugging purposes (check PHP-FPM error log)
        error_log("Database connection error: " . $e->getMessage());
        http_response_code(500); // Internal Server Error
        echo json_encode(["error" => "Could not connect to the database."]);
        exit(); // Terminate script execution
    }
}

// --- API Data Formatting Helper ---
function formatSensorDataFromRow($row, $includeDate = true) {
    // Create a DateTime object from the full timestamp string from the database
    // The timestamp format from sensor_collector.py is 'Y-m-d H:i:s.u'
    $dtObject = new DateTime($row['timestamp']);
    
    // Format time to exclude seconds and microseconds (e.g., "14:30")
    $formattedTime = $dtObject->format('H:i'); // H for 24-hour format, i for minutes
    
    // Prepare the basic formatted data array
    $formattedData = [
        "time"  => $formattedTime,
        // Convert temperature and humidity to string as requested by the user
        "temp"  => (string)round($row['temperature'], 2), // Round temperature to 2 decimal places
        "humid" => (string)(int)$row['humidity'] // Ensure humidity is an integer before converting to string
    ];

    // Conditionally add the 'date' field if requested
    if ($includeDate) {
        $formattedData["date"] = $dtObject->format('Y-m-d'); // Format date as "YYYY-MM-DD"
    }
    
    return $formattedData;
}

// --- API Endpoint Routing ---
// Get the requested URI (e.g., "/api/history?limit=5")
$requestUri = $_SERVER['REQUEST_URI'];
// Get the HTTP request method (e.g., "GET")
$requestMethod = $_SERVER['REQUEST_METHOD'];

// Parse the URL path, removing any query strings
$path = parse_url($requestUri, PHP_URL_PATH);
// Remove any trailing slashes from the path for consistent routing
$path = rtrim($path, '/');

// Simple router to direct requests to the appropriate handler function
if ($requestMethod === 'GET') {
    // Handle /api/history endpoint
    if ($path === '/api/history') {
        handleHistoryEndpoint();
    } 
    // Handle /api/latest endpoint
    elseif ($path === '/api/latest') {
        handleLatestEndpoint();
    } 
    // Handle /api/daily_history/{date_str} endpoint using regular expression
    elseif (preg_match('/^\/api\/daily_history\/(\d{4}-\d{2}-\d{2})$/', $path, $matches)) {
        // $matches[1] will contain the date string (e.g., "2025-07-28")
        handleDailyHistoryEndpoint($matches[1]);
    } 
    // If no matching endpoint is found
    else {
        http_response_code(404); // Not Found
        echo json_encode(["error" => "Endpoint not found."]);
    }
} 
// If the request method is not GET
else {
    http_response_code(405); // Method Not Allowed
    echo json_encode(["error" => "Method not allowed."]);
}

// --- Endpoint Handler Functions ---

function handleHistoryEndpoint() {
    $pdo = getDbConnection(); // Get a database connection

    // Get optional 'limit' and 'order' query parameters
    $limit = isset($_GET['limit']) ? (int)$_GET['limit'] : null;
    $order = isset($_GET['order']) ? strtolower($_GET['order']) : 'desc';

    // Validate the 'order' parameter
    if (!in_array($order, ['asc', 'desc'])) {
        http_response_code(400); // Bad Request
        echo json_encode(["error" => "Invalid 'order' parameter. Use 'asc' or 'desc'."]);
        return;
    }

    // Construct the base SQL query
    $query = "SELECT timestamp, temperature, humidity FROM sensor_readings ORDER BY timestamp " . ($order === 'asc' ? 'ASC' : 'DESC');
    
    // Add LIMIT clause if 'limit' parameter is provided
    if ($limit !== null) {
        $query .= " LIMIT :limit";
    }

    try {
        $stmt = $pdo->prepare($query); // Prepare the SQL statement
        
        // Bind the limit parameter if it's set
        if ($limit !== null) {
            $stmt->bindParam(':limit', $limit, PDO::PARAM_INT);
        }
        
        $stmt->execute(); // Execute the query
        $rows = $stmt->fetchAll(); // Fetch all results

        $data = [];
        // Loop through each row and format it using the helper function
        foreach ($rows as $row) {
            $data[] = formatSensorDataFromRow($row, true); // Include date for history endpoint
        }
        
        http_response_code(200); // OK
        echo json_encode($data); // Output JSON response
    } catch (PDOException $e) {
        error_log("Error fetching all sensor data: " . $e->getMessage());
        http_response_code(500); // Internal Server Error
        echo json_encode(["error" => "Could not retrieve sensor data."]);
    }
}

function handleLatestEndpoint() {
    $pdo = getDbConnection(); // Get a database connection

    try {
        // Query to get the single latest sensor reading
        $stmt = $pdo->query("SELECT timestamp, temperature, humidity FROM sensor_readings ORDER BY timestamp DESC LIMIT 1");
        $row = $stmt->fetch(); // Fetch the single row

        if ($row) {
            // Format the latest data, excluding the date as requested for this endpoint
            $latestData = formatSensorDataFromRow($row, false);
            http_response_code(200); // OK
            echo json_encode($latestData); // Output JSON response
        } else {
            http_response_code(404); // Not Found
            echo json_encode(["message" => "No sensor data found."]);
        }
    } catch (PDOException $e) {
        error_log("Error fetching latest sensor data: " . $e->getMessage());
        http_response_code(500); // Internal Server Error
        echo json_encode(["error" => "Could not retrieve latest sensor data."]);
    }
}

function handleDailyHistoryEndpoint($dateStr) {
    $pdo = getDbConnection(); // Get a database connection

    // Validate the date string format (YYYY-MM-DD)
    if (!DateTime::createFromFormat('Y-m-d', $dateStr)) {
        http_response_code(400); // Bad Request
        echo json_encode(["error" => "Invalid date format. Please use YYYY-MM-DD."]);
        return;
    }

    // Define the start and end timestamps for the given day
    $startOfDay = $dateStr . " 00:00:00.000000";
    $endOfDay = $dateStr . " 23:59:59.999999";

    // SQL query to select data within the specified date range
    $query = "
        SELECT timestamp, temperature, humidity 
        FROM sensor_readings 
        WHERE timestamp BETWEEN :start_of_day AND :end_of_day 
        ORDER BY timestamp ASC
    ";
    
    try {
        $stmt = $pdo->prepare($query); // Prepare the SQL statement
        $stmt->bindParam(':start_of_day', $startOfDay); // Bind start of day parameter
        $stmt->bindParam(':end_of_day', $endOfDay);    // Bind end of day parameter
        $stmt->execute(); // Execute the query
        $rows = $stmt->fetchAll(); // Fetch all results

        if (empty($rows)) {
            http_response_code(404); // Not Found
            echo json_encode(["message" => "No sensor data found for " . $dateStr . "."]);
            return;
        }
        
        $data = [];
        // Loop through each row and format it, including the date
        foreach ($rows as $row) {
            $data[] = formatSensorDataFromRow($row, true);
        }
        
        http_response_code(200); // OK
        echo json_encode($data); // Output JSON response
    } catch (PDOException $e) {
        error_log("Error fetching daily sensor data for " . $dateStr . ": " . $e->getMessage());
        http_response_code(500); // Internal Server Error
        echo json_encode(["error" => "Could not retrieve daily sensor data."]);
    }
}

?>
