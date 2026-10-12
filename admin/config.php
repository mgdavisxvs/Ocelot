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

        // Find most recent shard — Go names shards ocelot-YYYY-MM.db (hyphen)
        $shards = glob($shardDir . '/ocelot-*.db');
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
        $shards = glob($shardDir . '/ocelot-*.db'); // hyphen matches Go shard names
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

// ---------------------------------------------------------------------------
// Session hardening (FV-05)
//
// Cookie parameters must be set BEFORE session_start() or they are ignored.
// httponly keeps the session cookie out of reach of any script on the page;
// samesite=Lax is defence in depth BEHIND the CSRF token below, never instead
// of it. 'secure' is set only when the request actually arrived over TLS, so
// this still works on a plain-HTTP development host.
// ---------------------------------------------------------------------------
$isHttps = (!empty($_SERVER['HTTPS']) && strtolower($_SERVER['HTTPS']) !== 'off')
        || (($_SERVER['HTTP_X_FORWARDED_PROTO'] ?? '') === 'https');

if (session_status() === PHP_SESSION_NONE) {
    session_set_cookie_params([
        'lifetime' => 0,
        'path'     => '/',
        'httponly' => true,
        'secure'   => $isHttps,
        'samesite' => 'Lax',
    ]);
    session_start();
}

// ---------------------------------------------------------------------------
// CSRF protection (FV-06)
//
// Every state-mutating request must present a token bound to the session.
// Browsers will happily send the session cookie on a cross-site request; they
// will not send this token, because an attacker's page cannot read it.
// ---------------------------------------------------------------------------

/** Per-session CSRF token, minted on first use. */
function csrf_token(): string
{
    if (empty($_SESSION['csrf_token'])) {
        $_SESSION['csrf_token'] = bin2hex(random_bytes(32));
    }
    return $_SESSION['csrf_token'];
}

/** Hidden input carrying the token. Place inside every mutating <form>. */
function csrf_field(): string
{
    return '<input type="hidden" name="csrf_token" value="'
         . htmlspecialchars(csrf_token(), ENT_QUOTES) . '">';
}

/** Constant-time comparison against the session token. */
function csrf_verify(?string $token): bool
{
    return is_string($token)
        && $token !== ''
        && !empty($_SESSION['csrf_token'])
        && hash_equals($_SESSION['csrf_token'], $token);
}

/**
 * Reject the request unless it carries a valid token.
 *
 * Accepts the token from a form field or from the X-CSRF-Token header, so
 * fetch()-driven endpoints are covered by the same check as <form> posts.
 */
function csrf_require(bool $asJson = false): void
{
    $token = $_POST['csrf_token'] ?? $_SERVER['HTTP_X_CSRF_TOKEN'] ?? '';
    if (csrf_verify(is_string($token) ? $token : '')) {
        return;
    }
    http_response_code(403);
    if ($asJson) {
        header('Content-Type: application/json');
        echo json_encode(['error' => 'CSRF token missing or invalid']);
    } else {
        header('Content-Type: text/plain; charset=utf-8');
        echo "403 Forbidden - CSRF token missing or invalid.";
    }
    exit;
}

// ---------------------------------------------------------------------------
// Output encoding helpers (FV-04 / R-01)
//
// Distinct helpers per output context, because the contexts are not
// interchangeable: HTML text, URL query parameter, and JS numeric literal each
// require a different encoder. Named short so that escaping is the path of
// least resistance at the call site.
// ---------------------------------------------------------------------------

/** Escape for HTML text or a quoted attribute value. */
function e($value): string
{
    return htmlspecialchars((string) $value, ENT_QUOTES, 'UTF-8');
}

/** Escape for use inside a URL query string. */
function eu($value): string
{
    return urlencode((string) $value);
}

function requireAuth() {
    if (!isset($_SESSION['authenticated']) || $_SESSION['authenticated'] !== true) {
        header('Location: login.php');
        exit;
    }
}

function isAuthenticated() {
    return isset($_SESSION['authenticated']) && $_SESSION['authenticated'] === true;
}
