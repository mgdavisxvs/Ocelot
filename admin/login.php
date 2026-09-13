<?php
require_once 'config.php';

$error = '';

if ($_SERVER['REQUEST_METHOD'] === 'POST') {
    $username = $_POST['username'] ?? '';
    $password = $_POST['password'] ?? '';

    if ($username === ADMIN_USER && password_verify($password, ADMIN_PASS)) {
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
<html lang="en" class="h-full bg-gray-900">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Login - Ocelot Tracker Admin</title>
    <script src="https://cdn.tailwindcss.com"></script>
    <script src="https://unpkg.com/lucide@latest"></script>
</head>
<body class="h-full">
    <div class="flex min-h-full flex-col justify-center px-6 py-12 lg:px-8">
        <div class="sm:mx-auto sm:w-full sm:max-w-sm">
            <h1 class="text-center text-4xl mb-4">🐆</h1>
            <h2 class="text-center text-2xl font-bold leading-9 tracking-tight text-white">
                Ocelot Tracker Admin
            </h2>
        </div>

        <div class="mt-10 sm:mx-auto sm:w-full sm:max-w-sm">
            <?php if ($error): ?>
            <div class="mb-4 rounded-md bg-red-900 border border-red-700 p-4">
                <div class="flex">
                    <i data-lucide="alert-circle" class="h-5 w-5 text-red-400"></i>
                    <div class="ml-3">
                        <p class="text-sm text-red-200"><?= htmlspecialchars($error) ?></p>
                    </div>
                </div>
            </div>
            <?php endif; ?>

            <form class="space-y-6" method="POST">
                <div>
                    <label for="username" class="block text-sm font-medium leading-6 text-gray-300">Username</label>
                    <div class="mt-2">
                        <input id="username" name="username" type="text" required
                               class="block w-full rounded-md border-0 bg-gray-800 py-1.5 text-white shadow-sm ring-1 ring-inset ring-gray-700 focus:ring-2 focus:ring-inset focus:ring-blue-600 sm:text-sm sm:leading-6 px-3">
                    </div>
                </div>

                <div>
                    <label for="password" class="block text-sm font-medium leading-6 text-gray-300">Password</label>
                    <div class="mt-2">
                        <input id="password" name="password" type="password" required
                               class="block w-full rounded-md border-0 bg-gray-800 py-1.5 text-white shadow-sm ring-1 ring-inset ring-gray-700 focus:ring-2 focus:ring-inset focus:ring-blue-600 sm:text-sm sm:leading-6 px-3">
                    </div>
                </div>

                <div>
                    <button type="submit"
                            class="flex w-full justify-center rounded-md bg-blue-600 px-3 py-1.5 text-sm font-semibold leading-6 text-white shadow-sm hover:bg-blue-500 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-600">
                        Sign in
                    </button>
                </div>
            </form>

            <p class="mt-10 text-center text-sm text-gray-500">
                Default: admin / changeme
            </p>
        </div>
    </div>

    <script>
        lucide.createIcons();
    </script>
</body>
</html>
