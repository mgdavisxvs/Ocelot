<?php
require_once 'config.php';

// Logout is a state change, so it carries a token like any other. Without it
// a third-party page can sign the operator out at will - a nuisance rather
// than a breach, but the fix costs one query parameter.
if (!csrf_verify($_GET['csrf_token'] ?? '')) {
    http_response_code(403);
    header('Content-Type: text/plain; charset=utf-8');
    exit('403 Forbidden - CSRF token missing or invalid.');
}

// Clear the data, then the cookie, then the session itself. session_destroy()
// alone leaves both $_SESSION and the client cookie in place.
$_SESSION = [];

if (ini_get('session.use_cookies')) {
    $p = session_get_cookie_params();
    setcookie(session_name(), '', [
        'expires'  => time() - 42000,
        'path'     => $p['path'],
        'domain'   => $p['domain'],
        'secure'   => $p['secure'],
        'httponly' => $p['httponly'],
        'samesite' => $p['samesite'] ?? 'Lax',
    ]);
}

session_destroy();
header('Location: login.php');
exit;
