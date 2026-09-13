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

// ---------------------------------------------------------------------------
// Front-end asset delivery policy
//
// 'local' (default, recommended): serve the pinned, vendored copies from this
//   origin. No third-party origin can execute script inside an authenticated
//   admin page, and a CDN outage cannot blank the UI. Still zero build step -
//   the vendored files are the same artifacts the CDN serves.
//
// 'cdn': restore third-party delivery. Pinned to the exact versions below and
//   guarded by Subresource Integrity, so a substituted payload is refused by
//   the browser rather than executed. Strictly weaker than 'local': it trades
//   availability and supply-chain risk for origin bandwidth.
//
// Switching is a one-line change. Nothing else in the panel depends on it.
define('ASSET_MODE', 'local');

// Pinned front-end dependencies. SRI digests were computed from the exact
// bytes served by each CDN at pin time; regenerate with tools/regen-assets.sh
// whenever a version here changes, or 'cdn' mode will refuse to load.
const ASSETS = [
    'tailwind' => [
        'local' => 'assets/vendor/tailwind-3.4.16.js',
        'cdn'   => 'https://cdn.tailwindcss.com/3.4.16',
        'sri'   => 'sha384-mS5Uq7sE90lgbBDN8xgf34ibEgbZo4gB3tfLY40ZRle+M188BQw8onzNHg6GUZaA',
        'defer' => false,
    ],
    'alpine' => [
        'local' => 'assets/vendor/alpine-3.14.9.js',
        'cdn'   => 'https://cdn.jsdelivr.net/npm/alpinejs@3.14.9/dist/cdn.min.js',
        'sri'   => 'sha384-9Ax3MmS9AClxJyd5/zafcXXjxmwFhZCdsT6HJoJjarvCaAkJlk5QDzjLJm+Wdx5F',
        'defer' => true,
    ],
    'd3' => [
        'local' => 'assets/vendor/d3-7.9.0.js',
        'cdn'   => 'https://cdn.jsdelivr.net/npm/d3@7.9.0/dist/d3.min.js',
        'sri'   => 'sha384-CjloA8y00+1SDAUkjs099PVfnY2KmDC2BZnws9kh8D/lX1s46w6EPhpXdqMfjK6i',
        'defer' => false,
    ],
];

/**
 * Emit a <script> tag for a pinned dependency under the active ASSET_MODE.
 *
 * In 'cdn' mode the tag always carries integrity + crossorigin. There is no
 * code path that emits an unpinned or unguarded third-party script.
 */
function asset_script(string $key): string
{
    $a = ASSETS[$key] ?? null;
    if ($a === null) {
        return '';
    }
    $defer = $a['defer'] ? ' defer' : '';
    if (ASSET_MODE === 'cdn') {
        return '<script' . $defer . ' src="' . htmlspecialchars($a['cdn'], ENT_QUOTES)
             . '" integrity="' . htmlspecialchars($a['sri'], ENT_QUOTES)
             . '" crossorigin="anonymous" referrerpolicy="no-referrer"></script>';
    }
    return '<script' . $defer . ' src="' . htmlspecialchars($a['local'], ENT_QUOTES) . '"></script>';
}

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

// Load Tracker API client (new JSON-based implementation)
require_once __DIR__ . '/api/tracker-api.php';

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
