<?php
// Ocelot Tracker Admin - Configuration

// Database Configuration
// Use absolute path or __DIR__ relative path for reliability
$dbPath = __DIR__ . '/../data/db';
if (!is_dir($dbPath)) {
    // Try creating the directory if it doesn't exist
    @mkdir($dbPath, 0755, true);
}
define('DB_PATH', $dbPath);
// Credentials and URLs are read from environment variables.
// Set them before starting the web server; never hardcode secrets here.
$trackerUrl = getenv('TRACKER_URL') ?: 'http://localhost:34000';
$sitePassword = getenv('SITE_PASSWORD') ?: '';
$adminUser = getenv('ADMIN_USER') ?: 'admin';
$adminPassHash = getenv('ADMIN_PASS_HASH') ?: ''; // bcrypt hash via: php -r "echo password_hash('yourpass', PASSWORD_BCRYPT);"

// Refuse insecure defaults
if ($sitePassword === '' || $sitePassword === 'changeme') {
    http_response_code(500);
    die('FATAL: SITE_PASSWORD environment variable is not set or uses the default value.');
}
if ($adminPassHash === '') {
    http_response_code(500);
    die('FATAL: ADMIN_PASS_HASH environment variable is not set.');
}

define('TRACKER_URL', $trackerUrl);
define('SITE_PASSWORD', $sitePassword);

// Markov analytics sidecar URL (set MARKOV_URL env var to enable)
$markovUrl = getenv('MARKOV_URL') ?: 'http://localhost:9090';
define('MARKOV_URL', $markovUrl);

// Application Settings
define('ITEMS_PER_PAGE', 50);
define('SESSION_TIMEOUT', 3600);

// Admin Authentication
define('ADMIN_USER', $adminUser);
define('ADMIN_PASS', $adminPassHash);

// Timezone
date_default_timezone_set('UTC');

// Database Helper - Connects to current or specific shard
class OcelotDB {
    private static $connections = [];

    public static function getCurrentShard() {
        $shardDir = DB_PATH;
        if (!is_dir($shardDir)) {
            throw new Exception("Database directory not found: $shardDir");
        }

        // Find most recent shard
        $shards = glob($shardDir . '/ocelot_*.db');
        if (empty($shards)) {
            throw new Exception("No database shards found");
        }

        rsort($shards); // Most recent first
        return $shards[0];
    }

    public static function connect($shardPath = null) {
        if ($shardPath === null) {
            $shardPath = self::getCurrentShard();
        }

        if (!isset(self::$connections[$shardPath])) {
            try {
                $pdo = new PDO('sqlite:' . $shardPath);
                $pdo->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);
                $pdo->setAttribute(PDO::ATTR_DEFAULT_FETCH_MODE, PDO::FETCH_ASSOC);
                self::$connections[$shardPath] = $pdo;
            } catch (PDOException $e) {
                throw new Exception("Database connection failed: " . $e->getMessage());
            }
        }

        return self::$connections[$shardPath];
    }

    public static function getAllShards() {
        $shardDir = DB_PATH;
        $shards = glob($shardDir . '/ocelot_*.db');
        rsort($shards);
        return $shards;
    }

    // Query across all shards and aggregate results
    public static function queryAllShards($query, $params = []) {
        $results = [];
        foreach (self::getAllShards() as $shard) {
            try {
                $db = self::connect($shard);
                $stmt = $db->prepare($query);
                $stmt->execute($params);
                $results = array_merge($results, $stmt->fetchAll());
            } catch (PDOException $e) {
                // Skip shards that don't have the table yet
                continue;
            }
        }
        return $results;
    }
}

// Load API clients
require_once __DIR__ . '/api/tracker-api.php';
require_once __DIR__ . '/api/markov-api.php';

// Utility Functions
function formatBytes($bytes, $precision = 2) {
    $units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];

    for ($i = 0; $bytes > 1024 && $i < count($units) - 1; $i++) {
        $bytes /= 1024;
    }

    return round($bytes, $precision) . ' ' . $units[$i];
}

function timeAgo($timestamp) {
    $diff = time() - $timestamp;

    if ($diff < 60) return $diff . 's ago';
    if ($diff < 3600) return floor($diff / 60) . 'm ago';
    if ($diff < 86400) return floor($diff / 3600) . 'h ago';
    if ($diff < 604800) return floor($diff / 86400) . 'd ago';

    return date('Y-m-d H:i', $timestamp);
}

// Simple session-based auth
session_start();

function requireAuth() {
    if (!isset($_SESSION['authenticated']) || $_SESSION['authenticated'] !== true) {
        header('Location: login.php');
        exit;
    }
}

function isAuthenticated() {
    return isset($_SESSION['authenticated']) && $_SESSION['authenticated'] === true;
}
