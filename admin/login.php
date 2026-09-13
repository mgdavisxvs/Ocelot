<?php
require_once 'config.php';
require_once 'includes/icons.php';

$error = '';

if ($_SERVER['REQUEST_METHOD'] === 'POST') {
    // Login itself is CSRF-protected: without this, a third-party page can
    // silently sign the operator into an account the attacker controls.
    csrf_require();

    $username = $_POST['username'] ?? '';
    $password = $_POST['password'] ?? '';

    if ($username === ADMIN_USER && password_verify($password, ADMIN_PASS)) {
        // FV-05: a session id issued before authentication must never carry
        // privilege. Regenerating here invalidates any id an attacker planted
        // in the browser beforehand (session fixation).
        session_regenerate_id(true);

        // The CSRF token is bound to the session, so it is re-minted with it.
        unset($_SESSION['csrf_token']);

        $_SESSION['authenticated'] = true;
        $_SESSION['username'] = $username;
        header('Location: index.php');
        exit;
    } else {
        $error = 'Invalid credentials';
    }
}

if (isAuthenticated()) {
    header('Location: index.php');
    exit;
}
?>
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Login - Ocelot Tracker Admin</title>
    <?php
    /*
     * DELIBERATELY ZERO THIRD-PARTY RESOURCES ON THIS PAGE.
     *
     * This document contains a password field. Any script it loads executes in
     * the same document as that field and can read it keystroke by keystroke.
     * A floating tag such as `lucide@latest` or an unversioned CDN endpoint
     * hands that capability to whoever controls the upstream package - which
     * is why the previous version of this file was a credential-disclosure
     * defect, not merely a supply-chain concern.
     *
     * Everything below is inline and same-origin: hand-written CSS instead of
     * the Tailwind CDN, and server-rendered inline SVG instead of an icon
     * library. Do not add a <script src> or a remote stylesheet to this file.
     */
    ?>
    <style>
        :root {
            --bg:       #111827;
            --surface:  #1f2937;
            --border:   #374151;
            --text:     #f9fafb;
            --muted:    #d1d5db;
            --subtle:   #6b7280;
            --accent:   #2563eb;
            --accent-h: #3b82f6;
            --err-bg:   #7f1d1d;
            --err-bd:   #b91c1c;
            --err-ic:   #f87171;
            --err-tx:   #fecaca;
        }
        * { box-sizing: border-box; }
        html, body { height: 100%; }
        body {
            margin: 0;
            background: var(--bg);
            color: var(--text);
            font-family: ui-sans-serif, system-ui, -apple-system, "Segoe UI",
                         Roboto, "Helvetica Neue", Arial, sans-serif;
            display: flex;
            align-items: center;
            justify-content: center;
            padding: 3rem 1.5rem;
        }
        .card { width: 100%; max-width: 24rem; }
        .mark { text-align: center; font-size: 2.25rem; margin: 0 0 .75rem; }
        .title {
            text-align: center; font-size: 1.5rem; font-weight: 700;
            line-height: 2.25rem; letter-spacing: -.025em; margin: 0 0 2.5rem;
        }
        .alert {
            display: flex; gap: .75rem; align-items: flex-start;
            background: var(--err-bg); border: 1px solid var(--err-bd);
            border-radius: .375rem; padding: 1rem; margin-bottom: 1rem;
        }
        .alert svg { color: var(--err-ic); flex: 0 0 auto; width: 1.25rem; height: 1.25rem; }
        .alert p { margin: 0; font-size: .875rem; color: var(--err-tx); }
        .field { margin-bottom: 1.5rem; }
        label {
            display: block; font-size: .875rem; font-weight: 500;
            line-height: 1.5rem; color: var(--muted); margin-bottom: .5rem;
        }
        input[type="text"], input[type="password"] {
            display: block; width: 100%; padding: .375rem .75rem;
            background: var(--surface); color: var(--text);
            border: 0; border-radius: .375rem;
            box-shadow: inset 0 0 0 1px var(--border);
            font-size: .875rem; line-height: 1.5rem;
        }
        input[type="text"]:focus, input[type="password"]:focus {
            outline: none; box-shadow: inset 0 0 0 2px var(--accent-h);
        }
        button {
            display: flex; width: 100%; justify-content: center;
            padding: .375rem .75rem; border: 0; border-radius: .375rem;
            background: var(--accent); color: #fff;
            font-size: .875rem; font-weight: 600; line-height: 1.5rem;
            cursor: pointer;
        }
        button:hover { background: var(--accent-h); }
        button:focus-visible { outline: 2px solid var(--accent-h); outline-offset: 2px; }
        .foot {
            margin-top: 2.5rem; text-align: center;
            font-size: .875rem; color: var(--subtle);
        }
    </style>
</head>
<body>
    <main class="card">
        <p class="mark">&#128006;</p>
        <h1 class="title">Ocelot Tracker Admin</h1>

        <?php if ($error): ?>
        <div class="alert" role="alert">
            <?= icon('alert-circle') ?>
            <p><?= htmlspecialchars($error) ?></p>
        </div>
        <?php endif; ?>

        <form method="POST" autocomplete="off">
            <?= csrf_field() ?>
            <div class="field">
                <label for="username">Username</label>
                <input id="username" name="username" type="text" required autofocus>
            </div>

            <div class="field">
                <label for="password">Password</label>
                <input id="password" name="password" type="password" required>
            </div>

            <button type="submit">Sign in</button>
        </form>

        <p class="foot">Ocelot Tracker</p>
    </main>
</body>
</html>
