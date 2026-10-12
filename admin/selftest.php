<?php
/**
 * Ocelot Admin - front-end supply-chain self-tests.
 *
 * Asserts the invariants established when defects D-01..D-04 and the latent
 * FV-04 were remediated. Run from the repository root:
 *
 *     php admin/selftest.php
 *
 * Exit status 0 = all invariants hold, 1 = at least one regressed.
 * No external test runner, no Composer - consistent with the stack.
 */

// Loaded before any output: config.php calls session_start(), which emits a
// warning if headers have already been sent.
require_once __DIR__ . '/config.php';
require_once __DIR__ . '/includes/icons.php';

$pass = 0;
$fail = 0;

function check(string $name, bool $ok, string $detail = ''): void
{
    global $pass, $fail;
    if ($ok) {
        $pass++;
        echo "  PASS  $name\n";
    } else {
        $fail++;
        echo "  FAIL  $name" . ($detail !== '' ? " -- $detail" : '') . "\n";
    }
}

$root  = __DIR__;
$pages = glob($root . '/*.php');
$incs  = glob($root . '/includes/*.php');
$all   = array_merge($pages, $incs, glob($root . '/api/*.php'));
// The scanner quotes the very patterns it hunts for, so it must not scan
// itself - otherwise every check fails on its own source text.
$all   = array_values(array_filter($all, fn($f) => basename($f) !== 'selftest.php'));

echo "\nD-01  login page must load nothing from a third party\n";
$login = file_get_contents($root . '/login.php');
check('login.php has no remote <script src>',
    !preg_match('~<script[^>]+src\s*=\s*["\']https?://~i', $login));
check('login.php has no remote stylesheet',
    !preg_match('~<link[^>]+href\s*=\s*["\']https?://~i', $login));
check('login.php still renders its error icon',
    str_contains($login, "icon('alert-circle')"));
check('login.php no longer advertises default credentials',
    !str_contains($login, 'changeme'));

echo "\nD-02/D-03  no unpinned or unguarded third-party script anywhere\n";
foreach ($all as $f) {
    $src  = file_get_contents($f);
    $name = str_replace($root . '/', '', $f);
    if (preg_match_all('~<script[^>]+src\s*=\s*["\'](https?://[^"\']+)["\'][^>]*>~i', $src, $m, PREG_SET_ORDER)) {
        foreach ($m as $tag) {
            check("$name: '{$tag[1]}' carries integrity=",
                str_contains($tag[0], 'integrity='), 'unguarded third-party script');
        }
    }
    // Scope this to real URL attributes. Matching raw file text would flag
    // the comments that explain why the floating tags were removed.
    preg_match_all('~(?:src|href)\s*=\s*["\']([^"\']+)["\']~i', $src, $urls);
    $floating = array_values(array_filter($urls[1],
        fn($u) => (bool) preg_match('~(@latest|@\d+\.x|\.x\.x)~', $u)));
    check("$name: no floating version specifier in any src/href",
        $floating === [], implode(', ', $floating));
}

echo "\nD-04  vendored fallbacks present and byte-matched to their SRI digests\n";
foreach (ASSETS as $key => $a) {
    $path = $root . '/' . $a['local'];
    $ok   = is_file($path);
    check("$key: vendored copy exists", $ok, $a['local']);
    if ($ok) {
        $digest = 'sha384-' . base64_encode(hash('sha384', file_get_contents($path), true));
        check("$key: vendored bytes match pinned SRI digest", hash_equals($a['sri'], $digest),
            'vendored file and CDN pin have diverged');
    }
}
check("ASSET_MODE is a known value", in_array(ASSET_MODE, ['local', 'cdn'], true), ASSET_MODE);

echo "\nIcon set  whitelist integrity and injection resistance\n";
$referenced = [];
foreach ($all as $f) {
    if (preg_match_all("~icon\(\s*'([a-z0-9-]+)'~", file_get_contents($f), $m)) {
        $referenced = array_merge($referenced, $m[1]);
    }
}
$referenced = array_unique($referenced);
check('every statically referenced icon exists (' . count($referenced) . ' names)',
    count(array_filter($referenced, fn($n) => !icon_exists($n))) === 0,
    implode(', ', array_filter($referenced, fn($n) => !icon_exists($n))));
check('peers.php dynamic icon names both resolve',
    icon_exists('arrow-up') && icon_exists('arrow-down'));
check('unknown icon name renders nothing, not raw input',
    icon('"><script>alert(1)</script>') === '');
check('icon class attribute is escaped',
    !str_contains(icon('database', '"><script>x</script>'), '<script>'));
check('legacy Lucide aliases still resolve after upstream rename',
    icon_exists('circle-alert') && icon_exists('circle-check'));
check('no data-lucide markup survives anywhere',
    count(array_filter($all, fn($f) => str_contains(file_get_contents($f), 'data-lucide'))) === 0);

echo "\nFV-04  output escaping at the page-title sink\n";
$hdr = file_get_contents($root . '/includes/header.php');
check('header.php escapes $pageTitle in <title>',
    (bool) preg_match('~<title><\?=\s*htmlspecialchars\(~', $hdr));

echo "\nFV-05  session fixation\n";
$login = file_get_contents($root . '/login.php');
check('login.php regenerates the session id on success',
    str_contains($login, 'session_regenerate_id(true)'));
check('login.php re-mints the CSRF token with the new session',
    str_contains($login, 'unset($_SESSION[') && str_contains($login, 'csrf_token'));
$cfg = file_get_contents($root . '/config.php');
check('session cookie is httponly', str_contains($cfg, "'httponly' => true"));
check('session cookie is samesite', str_contains($cfg, "'samesite' => 'Lax'"));
// Match the statement form, not the prose: the comment above the call also
// contains the words "session_start()" and would otherwise match first.
check('cookie params are set before session_start()',
    strpos($cfg, 'session_set_cookie_params') < strpos($cfg, 'session_start();'));
$logout = file_get_contents($root . '/logout.php');
check('logout clears $_SESSION, cookie and session',
    str_contains($logout, '$_SESSION = []')
    && str_contains($logout, 'setcookie(session_name()')
    && str_contains($logout, 'session_destroy()'));

echo "\nFV-06  CSRF token covers every state-mutating path\n";
check('csrf_verify rejects an empty token',   !csrf_verify(''));
check('csrf_verify rejects a wrong token',    !csrf_verify(str_repeat('a', 64)));
check('csrf_verify accepts the session token', csrf_verify(csrf_token()));
check('token is 256 bits of entropy, hex-encoded',
    strlen(csrf_token()) === 64 && ctype_xdigit(csrf_token()));
check('csrf_field embeds the live token',
    str_contains(csrf_field(), csrf_token()));
foreach (['users.php', 'torrents.php', 'login.php'] as $f) {
    $src = file_get_contents($root . '/' . $f);
    check("$f: POST handler calls csrf_require()", str_contains($src, 'csrf_require()'));
}
// Every POST form must carry the field. GET forms (filters) must not need it.
foreach (array_merge($pages, $incs) as $f) {
    $src  = file_get_contents($f);
    $name = str_replace($root . '/', '', $f);
    $posts = preg_match_all('~<form[^>]*method\s*=\s*["\']POST["\'][^>]*>~i', $src);
    if ($posts > 0) {
        check("$name: all $posts POST form(s) carry csrf_field()",
            substr_count($src, 'csrf_field()') >= $posts);
    }
}
check('logout.php requires a token',   str_contains($logout, 'csrf_verify'));
$api = file_get_contents($root . '/api/parse-torrent.php');
check('parse-torrent.php requires authentication', str_contains($api, 'isAuthenticated()'));
check('parse-torrent.php requires a CSRF token',   str_contains($api, 'csrf_require(true)'));
check('torrents.php sends the token with fetch()',
    str_contains(file_get_contents($root . '/torrents.php'), "'X-CSRF-Token': CSRF_TOKEN"));

echo "\nR-01  DB-derived output is encoded for its context\n";
check('e() escapes HTML metacharacters',
    e('<script>&"') === '&lt;script&gt;&amp;&quot;');
check('eu() percent-encodes URL metacharacters',
    eu('a b&c=d') === 'a+b%26c%3Dd');
$sinks = [];
foreach (array_merge($pages, $incs) as $f) {
    $src = file_get_contents($f);
    if (preg_match_all('~<\?=\s*\$(?:peer|torrent|user|row|snatch)\[[^\]]+\]\s*\?>~', $src, $m)) {
        foreach ($m[0] as $hit) {
            $sinks[] = str_replace($root . '/', '', $f) . ': ' . $hit;
        }
    }
}
check('no unescaped DB-derived echo remains (' . count($sinks) . ' found)',
    $sinks === [], implode(' | ', array_slice($sinks, 0, 3)));

echo "\nParser hardening (admin/api/parse-torrent.php)\n";
check('bdecode recursion is bounded',      str_contains($api, 'BDECODE_MAX_DEPTH'));
check('recursive calls propagate depth',   substr_count($api, '$depth + 1') === 3);
check('string length is range-checked',    str_contains($api, 'string length out of range'));
check('upload size is bounded',            str_contains($api, 'MAX_TORRENT_BYTES'));
check('upload provenance is verified',     str_contains($api, 'is_uploaded_file'));
check('formatBytes is not re-declared',    !preg_match('~^function formatBytes~m', $api));

$total = $pass + $fail;
echo "\n" . str_repeat('-', 58) . "\n";
printf("  %d/%d invariants hold%s\n", $pass, $total, $fail ? " -- $fail REGRESSED" : '');
echo str_repeat('-', 58) . "\n\n";
exit($fail === 0 ? 0 : 1);
