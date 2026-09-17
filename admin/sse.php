<?php
require_once 'config.php';
requireAuth();

// ── Admin SSE endpoint ──────────────────────────────────────────────────────
// Subscribes to Redis Pub/Sub channels and streams events as Server-Sent Events
// to the admin dashboard JavaScript client.

$redisUrl  = getenv('REDIS_URL')  ?: '';
$redisPwd  = getenv('REDIS_PASSWORD') ?: '';

if ($redisUrl === '') {
    http_response_code(503);
    header('Content-Type: application/json');
    echo json_encode(['error' => 'Redis not configured — SSE unavailable']);
    exit;
}

// Parse REDIS_URL (redis://[:password@]host[:port][/db])
$parts = parse_url($redisUrl);
$redisHost = $parts['host'] ?? '127.0.0.1';
$redisPort = $parts['port'] ?? 6379;
if ($redisPwd === '' && isset($parts['pass'])) {
    $redisPwd = $parts['pass'];
}

$redis = new Redis();
if (!@$redis->connect($redisHost, (int)$redisPort, 5.0)) {
    http_response_code(503);
    header('Content-Type: application/json');
    echo json_encode(['error' => 'Redis connection failed']);
    exit;
}
if ($redisPwd !== '') {
    $redis->auth($redisPwd);
}

$channels = [
    'ocelot:sse:events',       // outbound fan-out from tracker RedisBridge
    'ocelot:anomaly:client',
    'ocelot:anomaly:behaviour',
    'ocelot:user:flagged',
    'ocelot:user:unflagged',
    'ocelot:freeleech:recommended',
];

header('Content-Type: text/event-stream');
header('Cache-Control: no-cache');
header('Connection: keep-alive');
header('X-Accel-Buffering: no');

// Disable output buffering.
if (ob_get_level()) {
    ob_end_flush();
}

echo ": connected\n\n";
flush();

set_time_limit(0);
ignore_user_abort(true);

$redis->subscribe($channels, function (Redis $redis, string $channel, string $message) {
    if (connection_aborted()) {
        $redis->close();
        return false; // stops subscribe loop
    }

    $env = json_decode($message, true);
    if (!is_array($env)) {
        return true;
    }

    $out = json_encode([
        'topic'       => $env['topic'] ?? $channel,
        'occurred_at' => isset($env['occ_at']) ? intdiv((int)$env['occ_at'], 1000000) : (time() * 1000),
        'data'        => $env['payload'] ?? null,
    ]);

    echo "data: {$out}\n\n";
    flush();

    return true;
});
