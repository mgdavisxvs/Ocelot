<!DOCTYPE html>
<html lang="en" class="h-full bg-gray-900">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title><?= $pageTitle ?? 'Ocelot Tracker Admin' ?></title>

    <!-- Tailwind CSS -->
    <script src="https://cdn.tailwindcss.com"></script>
    <script>
        tailwind.config = {
            darkMode: 'class',
            theme: {
                extend: {
                    colors: {
                        primary: '#3b82f6',
                        secondary: '#8b5cf6'
                    }
                }
            }
        }
    </script>

    <!-- Alpine.js -->
    <script defer src="https://cdn.jsdelivr.net/npm/alpinejs@3.x.x/dist/cdn.min.js"></script>

    <!-- D3.js -->
    <script src="https://d3js.org/d3.v7.min.js"></script>

    <!-- Lucide Icons -->
    <script src="https://unpkg.com/lucide@latest"></script>

    <style>
        [x-cloak] { display: none !important; }
    </style>
</head>
<body class="h-full">
    <div class="min-h-full">
        <!-- Navigation -->
        <nav class="bg-gray-800 border-b border-gray-700" x-data="{ mobileMenuOpen: false }">
            <div class="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
                <div class="flex h-16 items-center justify-between">
                    <div class="flex items-center">
                        <div class="flex-shrink-0">
                            <span class="text-2xl">🐆</span>
                        </div>
                        <div class="hidden md:block">
                            <div class="ml-10 flex items-baseline space-x-4">
                                <a href="index.php" class="<?= basename($_SERVER['PHP_SELF']) === 'index.php' ? 'bg-gray-900 text-white' : 'text-gray-300 hover:bg-gray-700 hover:text-white' ?> rounded-md px-3 py-2 text-sm font-medium">
                                    Dashboard
                                </a>
                                <a href="torrents.php" class="<?= basename($_SERVER['PHP_SELF']) === 'torrents.php' ? 'bg-gray-900 text-white' : 'text-gray-300 hover:bg-gray-700 hover:text-white' ?> rounded-md px-3 py-2 text-sm font-medium">
                                    Torrents
                                </a>
                                <a href="users.php" class="<?= basename($_SERVER['PHP_SELF']) === 'users.php' ? 'bg-gray-900 text-white' : 'text-gray-300 hover:bg-gray-700 hover:text-white' ?> rounded-md px-3 py-2 text-sm font-medium">
                                    Users
                                </a>
                                <a href="peers.php" class="<?= basename($_SERVER['PHP_SELF']) === 'peers.php' ? 'bg-gray-900 text-white' : 'text-gray-300 hover:bg-gray-700 hover:text-white' ?> rounded-md px-3 py-2 text-sm font-medium">
                                    Peers
                                </a>
                                <a href="stats.php" class="<?= basename($_SERVER['PHP_SELF']) === 'stats.php' ? 'bg-gray-900 text-white' : 'text-gray-300 hover:bg-gray-700 hover:text-white' ?> rounded-md px-3 py-2 text-sm font-medium">
                                    Statistics
                                </a>
                            </div>
                        </div>
                    </div>
                    <div class="hidden md:block">
                        <div class="ml-4 flex items-center md:ml-6">
                            <span class="text-sm text-gray-400 mr-4">
                                <i data-lucide="database" class="inline-block w-4 h-4"></i>
                                <?php
                                try {
                                    $shards = OcelotDB::getAllShards();
                                    echo count($shards) . ' shard' . (count($shards) !== 1 ? 's' : '');
                                } catch (Exception $e) {
                                    echo 'DB offline';
                                }
                                ?>
                            </span>
                            <a href="logout.php" class="text-gray-300 hover:text-white">
                                <i data-lucide="log-out" class="w-5 h-5"></i>
                            </a>
                        </div>
                    </div>
                    <div class="-mr-2 flex md:hidden">
                        <button @click="mobileMenuOpen = !mobileMenuOpen" class="inline-flex items-center justify-center rounded-md bg-gray-800 p-2 text-gray-400 hover:bg-gray-700 hover:text-white">
                            <i data-lucide="menu" class="w-6 h-6"></i>
                        </button>
                    </div>
                </div>
            </div>

            <!-- Mobile menu -->
            <div x-show="mobileMenuOpen" x-cloak class="md:hidden">
                <div class="space-y-1 px-2 pb-3 pt-2 sm:px-3">
                    <a href="index.php" class="<?= basename($_SERVER['PHP_SELF']) === 'index.php' ? 'bg-gray-900 text-white' : 'text-gray-300 hover:bg-gray-700 hover:text-white' ?> block rounded-md px-3 py-2 text-base font-medium">Dashboard</a>
                    <a href="torrents.php" class="<?= basename($_SERVER['PHP_SELF']) === 'torrents.php' ? 'bg-gray-900 text-white' : 'text-gray-300 hover:bg-gray-700 hover:text-white' ?> block rounded-md px-3 py-2 text-base font-medium">Torrents</a>
                    <a href="users.php" class="<?= basename($_SERVER['PHP_SELF']) === 'users.php' ? 'bg-gray-900 text-white' : 'text-gray-300 hover:bg-gray-700 hover:text-white' ?> block rounded-md px-3 py-2 text-base font-medium">Users</a>
                    <a href="peers.php" class="<?= basename($_SERVER['PHP_SELF']) === 'peers.php' ? 'bg-gray-900 text-white' : 'text-gray-300 hover:bg-gray-700 hover:text-white' ?> block rounded-md px-3 py-2 text-base font-medium">Peers</a>
                    <a href="stats.php" class="<?= basename($_SERVER['PHP_SELF']) === 'stats.php' ? 'bg-gray-900 text-white' : 'text-gray-300 hover:bg-gray-700 hover:text-white' ?> block rounded-md px-3 py-2 text-base font-medium">Statistics</a>
                    <a href="logout.php" class="text-gray-300 hover:bg-gray-700 hover:text-white block rounded-md px-3 py-2 text-base font-medium">Logout</a>
                </div>
            </div>
        </nav>

        <!-- Page Header -->
        <?php if (isset($pageTitle)): ?>
        <header class="bg-gray-800 shadow">
            <div class="mx-auto max-w-7xl px-4 py-6 sm:px-6 lg:px-8">
                <h1 class="text-3xl font-bold tracking-tight text-white"><?= htmlspecialchars($pageTitle) ?></h1>
            </div>
        </header>
        <?php endif; ?>

        <!-- Main Content -->
        <main>
            <div class="mx-auto max-w-7xl py-6 sm:px-6 lg:px-8">
