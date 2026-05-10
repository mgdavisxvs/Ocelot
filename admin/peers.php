<?php
require_once 'config.php';
requireAuth();

$pageTitle = 'Active Peers';

// Pagination
$page = isset($_GET['page']) ? max(1, (int)$_GET['page']) : 1;
$offset = ($page - 1) * ITEMS_PER_PAGE;

// Filters
$filterTorrent = $_GET['torrent_id'] ?? '';
$filterUser = $_GET['user_id'] ?? '';
$filterType = $_GET['type'] ?? ''; // 'seeder' or 'leecher'

// Fetch peers from database
try {
    $db = OcelotDB::connect();

    // Build query with filters
    $whereClauses = ["timestamp > " . (time() - 7200)];
    $params = [];

    if ($filterTorrent !== '') {
        $whereClauses[] = "torrent_id = ?";
        $params[] = $filterTorrent;
    }

    if ($filterUser !== '') {
        $whereClauses[] = "user_id = ?";
        $params[] = $filterUser;
    }

    if ($filterType === 'seeder') {
        $whereClauses[] = "torrent_left = 0";
    } elseif ($filterType === 'leecher') {
        $whereClauses[] = "torrent_left > 0";
    }

    $whereClause = implode(' AND ', $whereClauses);

    // Count total
    $stmt = $db->prepare("SELECT COUNT(*) as total FROM peers WHERE $whereClause");
    $stmt->execute($params);
    $totalPeers = $stmt->fetch()['total'];
    $totalPages = ceil($totalPeers / ITEMS_PER_PAGE);

    // Fetch page
    $stmt = $db->prepare("
        SELECT user_id, torrent_id, peer_id, ip, port,
               uploaded, downloaded, torrent_left,
               up_speed, down_speed, timestamp, user_agent
        FROM peers
        WHERE $whereClause
        ORDER BY timestamp DESC
        LIMIT ? OFFSET ?
    ");

    foreach ($params as $i => $param) {
        $stmt->bindValue($i + 1, $param);
    }
    $stmt->bindValue(count($params) + 1, ITEMS_PER_PAGE, PDO::PARAM_INT);
    $stmt->bindValue(count($params) + 2, $offset, PDO::PARAM_INT);
    $stmt->execute();

    $peers = $stmt->fetchAll();

} catch (Exception $e) {
    $error = $e->getMessage();
    $peers = [];
    $totalPeers = 0;
    $totalPages = 0;
}

include 'includes/header.php';
?>

<!-- Filters -->
<div class="bg-gray-800 shadow rounded-lg border border-gray-700 p-4 mb-6">
    <form method="GET" class="grid grid-cols-1 md:grid-cols-4 gap-4">
        <div>
            <label class="block text-sm font-medium text-gray-300 mb-1">Torrent ID</label>
            <input type="number" name="torrent_id" value="<?= htmlspecialchars($filterTorrent) ?>"
                   placeholder="Filter by torrent"
                   class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500">
        </div>

        <div>
            <label class="block text-sm font-medium text-gray-300 mb-1">User ID</label>
            <input type="number" name="user_id" value="<?= htmlspecialchars($filterUser) ?>"
                   placeholder="Filter by user"
                   class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500">
        </div>

        <div>
            <label class="block text-sm font-medium text-gray-300 mb-1">Peer Type</label>
            <select name="type"
                    class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500">
                <option value="">All Peers</option>
                <option value="seeder" <?= $filterType === 'seeder' ? 'selected' : '' ?>>Seeders Only</option>
                <option value="leecher" <?= $filterType === 'leecher' ? 'selected' : '' ?>>Leechers Only</option>
            </select>
        </div>

        <div class="flex items-end">
            <button type="submit"
                    class="w-full px-4 py-2 bg-blue-600 text-white rounded-md hover:bg-blue-700">
                <i data-lucide="filter" class="inline w-4 h-4 mr-1"></i>
                Apply Filters
            </button>
        </div>
    </form>
</div>

<!-- Stats Summary -->
<div class="grid grid-cols-2 md:grid-cols-4 gap-4 mb-6">
    <div class="bg-gray-800 rounded-lg border border-gray-700 p-4 text-center">
        <div class="text-2xl font-bold text-white"><?= number_format($totalPeers) ?></div>
        <div class="text-sm text-gray-400">Total Peers</div>
    </div>
    <div class="bg-gray-800 rounded-lg border border-gray-700 p-4 text-center">
        <div class="text-2xl font-bold text-green-400"><?= number_format(array_sum(array_column($peers, 'torrent_left')) === 0 ? count($peers) : 0) ?></div>
        <div class="text-sm text-gray-400">Current Page Seeders</div>
    </div>
    <div class="bg-gray-800 rounded-lg border border-gray-700 p-4 text-center">
        <div class="text-2xl font-bold text-white">Page <?= $page ?></div>
        <div class="text-sm text-gray-400">of <?= $totalPages ?: 1 ?></div>
    </div>
    <div class="bg-gray-800 rounded-lg border border-gray-700 p-4 text-center">
        <div class="text-2xl font-bold text-blue-400"><?= formatBytes(array_sum(array_column($peers, 'uploaded'))) ?></div>
        <div class="text-sm text-gray-400">Page Total Upload</div>
    </div>
</div>

<!-- Peers Table -->
<div class="bg-gray-800 shadow rounded-lg border border-gray-700 overflow-hidden">
    <div class="overflow-x-auto">
        <table class="min-w-full divide-y divide-gray-700">
            <thead class="bg-gray-900">
                <tr>
                    <th class="px-4 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">User</th>
                    <th class="px-4 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">Torrent</th>
                    <th class="px-4 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">Type</th>
                    <th class="px-4 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">IP:Port</th>
                    <th class="px-4 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">↑ Uploaded</th>
                    <th class="px-4 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">↓ Downloaded</th>
                    <th class="px-4 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">Left</th>
                    <th class="px-4 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">Speed</th>
                    <th class="px-4 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">Last Seen</th>
                </tr>
            </thead>
            <tbody class="divide-y divide-gray-700">
                <?php if (empty($peers)): ?>
                <tr>
                    <td colspan="9" class="px-6 py-8 text-center text-gray-400">
                        <i data-lucide="wifi-off" class="w-12 h-12 mx-auto mb-2 opacity-50"></i>
                        <p>No active peers found</p>
                    </td>
                </tr>
                <?php else: ?>
                    <?php foreach ($peers as $peer): ?>
                    <?php
                    $isSeeder = $peer['torrent_left'] == 0;
                    $typeClass = $isSeeder ? 'text-green-400' : 'text-yellow-400';
                    $typeIcon = $isSeeder ? 'arrow-up' : 'arrow-down';
                    $typeLabel = $isSeeder ? 'Seeder' : 'Leecher';
                    ?>
                    <tr class="hover:bg-gray-750">
                        <td class="px-4 py-3 whitespace-nowrap text-sm text-blue-400">
                            <a href="?user_id=<?= $peer['user_id'] ?>" class="hover:underline">
                                <?= $peer['user_id'] ?>
                            </a>
                        </td>
                        <td class="px-4 py-3 whitespace-nowrap text-sm text-purple-400">
                            <a href="?torrent_id=<?= $peer['torrent_id'] ?>" class="hover:underline">
                                <?= $peer['torrent_id'] ?>
                            </a>
                        </td>
                        <td class="px-4 py-3 whitespace-nowrap text-sm <?= $typeClass ?>">
                            <i data-lucide="<?= $typeIcon ?>" class="inline w-4 h-4"></i>
                            <?= $typeLabel ?>
                        </td>
                        <td class="px-4 py-3 whitespace-nowrap text-sm text-gray-400 font-mono">
                            <?= htmlspecialchars($peer['ip']) ?>:<?= $peer['port'] ?>
                        </td>
                        <td class="px-4 py-3 whitespace-nowrap text-sm text-green-400">
                            <?= formatBytes($peer['uploaded']) ?>
                        </td>
                        <td class="px-4 py-3 whitespace-nowrap text-sm text-blue-400">
                            <?= formatBytes($peer['downloaded']) ?>
                        </td>
                        <td class="px-4 py-3 whitespace-nowrap text-sm text-yellow-400">
                            <?= formatBytes($peer['torrent_left']) ?>
                        </td>
                        <td class="px-4 py-3 whitespace-nowrap text-sm text-gray-300">
                            ↑<?= formatBytes($peer['up_speed']) ?>/s
                            <br>
                            ↓<?= formatBytes($peer['down_speed']) ?>/s
                        </td>
                        <td class="px-4 py-3 whitespace-nowrap text-sm text-gray-400" title="<?= date('Y-m-d H:i:s', $peer['timestamp']) ?>">
                            <?= timeAgo($peer['timestamp']) ?>
                        </td>
                    </tr>
                    <?php endforeach; ?>
                <?php endif; ?>
            </tbody>
        </table>
    </div>
</div>

<!-- Pagination -->
<?php if ($totalPages > 1): ?>
<div class="flex justify-between items-center mt-6">
    <div class="text-sm text-gray-400">
        Showing <?= number_format($offset + 1) ?> to <?= number_format(min($offset + ITEMS_PER_PAGE, $totalPeers)) ?>
        of <?= number_format($totalPeers) ?> peers
    </div>

    <div class="flex gap-2">
        <?php if ($page > 1): ?>
        <a href="?page=<?= $page - 1 ?>&torrent_id=<?= urlencode($filterTorrent) ?>&user_id=<?= urlencode($filterUser) ?>&type=<?= urlencode($filterType) ?>"
           class="px-4 py-2 bg-gray-700 text-white rounded-md hover:bg-gray-600">
            Previous
        </a>
        <?php endif; ?>

        <?php
        $startPage = max(1, $page - 2);
        $endPage = min($totalPages, $page + 2);

        for ($i = $startPage; $i <= $endPage; $i++):
            $activeClass = $i === $page ? 'bg-blue-600' : 'bg-gray-700 hover:bg-gray-600';
        ?>
        <a href="?page=<?= $i ?>&torrent_id=<?= urlencode($filterTorrent) ?>&user_id=<?= urlencode($filterUser) ?>&type=<?= urlencode($filterType) ?>"
           class="px-4 py-2 <?= $activeClass ?> text-white rounded-md">
            <?= $i ?>
        </a>
        <?php endfor; ?>

        <?php if ($page < $totalPages): ?>
        <a href="?page=<?= $page + 1 ?>&torrent_id=<?= urlencode($filterTorrent) ?>&user_id=<?= urlencode($filterUser) ?>&type=<?= urlencode($filterType) ?>"
           class="px-4 py-2 bg-gray-700 text-white rounded-md hover:bg-gray-600">
            Next
        </a>
        <?php endif; ?>
    </div>
</div>
<?php endif; ?>

<?php include 'includes/footer.php'; ?>
