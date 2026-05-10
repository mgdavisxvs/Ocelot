<?php
// Ocelot Tracker Admin - Configuration

// Database Configuration
define('DB_PATH', '../data/db'); // Path to SQLite shards
define('TRACKER_URL', 'http://localhost:34000'); // Tracker API endpoint
define('SITE_PASSWORD', 'changeme'); // Must match tracker config

// Application Settings
define('ITEMS_PER_PAGE', 50);
define('SESSION_TIMEOUT', 3600);

// Admin Authentication (in production, use proper auth)
define('ADMIN_USER', 'admin');
define('ADMIN_PASS', password_hash('changeme', PASSWORD_BCRYPT));

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

// API Helper - Communicate with tracker
class TrackerAPI {
    public static function request($endpoint, $data = []) {
        $url = TRACKER_URL . '/' . SITE_PASSWORD . '/' . $endpoint;

        if (!empty($data)) {
            $url .= '?' . http_build_query($data);
        }

        $ch = curl_init($url);
        curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
        curl_setopt($ch, CURLOPT_TIMEOUT, 5);

        $response = curl_exec($ch);
        $httpCode = curl_getinfo($ch, CURLINFO_HTTP_CODE);
        curl_close($ch);

        if ($httpCode !== 200) {
            throw new Exception("Tracker API error: HTTP $httpCode");
        }

        return $response;
    }

    public static function addTorrent($torrentID, $infoHash) {
        return self::request('update', [
            'action' => 'add_torrent',
            'id' => $torrentID,
            'info_hash' => $infoHash,
            'freetorrent' => 0
        ]);
    }

    public static function updateUser($userID, $passkey, $canLeech = true, $isProtected = false) {
        return self::request('update', [
            'action' => 'update_user',
            'id' => $userID,
            'passkey' => $passkey,
            'can_leech' => $canLeech ? 1 : 0,
            'protect_ip' => $isProtected ? 1 : 0
        ]);
    }

    public static function deleteUser($userID) {
        return self::request('update', [
            'action' => 'remove_user',
            'id' => $userID
        ]);
    }
}

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
