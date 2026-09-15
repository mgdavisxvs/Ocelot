<?php
/**
 * Server-side proxy for the Markov analytics sidecar.
 *
 * Keeps the internal sidecar address out of the browser and avoids CORS.
 * Only whitelisted path prefixes are forwarded.
 *
 * Usage: GET api/markov-proxy.php?path=/torrent/123
 */

require_once dirname(__DIR__) . '/config.php';
requireAuth();

$path = $_GET['path'] ?? '';

// Whitelist: only allow safe, read-only paths.
$allowed = ['/torrent/', '/user/', '/freeleech', '/seeder/pagerank', '/chain/', '/health', '/metrics'];
$permitted = false;
foreach ($allowed as $prefix) {
    if (str_starts_with($path, $prefix)) {
        $permitted = true;
        break;
    }
}

if (!$permitted || preg_match('#[^a-zA-Z0-9/_\-\.?=&]#', $path)) {
    http_response_code(400);
    echo json_encode(['error' => 'path not permitted']);
    exit;
}

$url = rtrim(MARKOV_URL, '/') . $path;

$ch = curl_init($url);
curl_setopt_array($ch, [
    CURLOPT_RETURNTRANSFER => true,
    CURLOPT_TIMEOUT        => 5,
    CURLOPT_CONNECTTIMEOUT => 3,
    CURLOPT_HTTPHEADER     => ['Accept: application/json'],
]);

$body     = curl_exec($ch);
$httpCode = curl_getinfo($ch, CURLINFO_HTTP_CODE);
curl_close($ch);

http_response_code($httpCode ?: 502);
header('Content-Type: application/json');
echo $body !== false ? $body : json_encode(['error' => 'sidecar unreachable']);
