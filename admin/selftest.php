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

$total = $pass + $fail;
echo "\n" . str_repeat('-', 58) . "\n";
printf("  %d/%d invariants hold%s\n", $pass, $total, $fail ? " -- $fail REGRESSED" : '');
echo str_repeat('-', 58) . "\n\n";
exit($fail === 0 ? 0 : 1);
