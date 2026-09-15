<?php
require_once 'config.php';
requireAuth();

$pageTitle = 'Audit Log';

// Filters from query string
$filterAction       = $_GET['action']        ?? '';
$filterResource     = $_GET['resource_type'] ?? '';
$filterSuccess      = $_GET['success']        ?? ''; // '1', '0', or ''
$filterDays         = max(1, min(90, (int)($_GET['days'] ?? 7)));
$page               = max(1, (int)($_GET['page'] ?? 1));
$limit              = 100;
$offset             = ($page - 1) * $limit;

$since = time() - ($filterDays * 86400);

// Build WHERE clauses
$wheres = ['timestamp >= ?'];
$params = [$since];

if ($filterAction !== '') {
    $wheres[] = 'action = ?';
    $params[]  = $filterAction;
}
if ($filterResource !== '') {
    $wheres[] = 'resource_type = ?';
    $params[]  = $filterResource;
}
if ($filterSuccess !== '') {
    $wheres[] = 'success = ?';
    $params[]  = (int)$filterSuccess;
}

$where = implode(' AND ', $wheres);

$entries      = [];
$totalEntries = 0;
$totalPages   = 1;
$actionValues = [];
$resourceValues = [];
$error        = '';

try {
    $db = OcelotDB::connect();

    // Check table exists
    $tableExists = $db->query(
        "SELECT name FROM sqlite_master WHERE type='table' AND name='audit_log'"
    )->fetch();

    if ($tableExists) {
        $stmt = $db->prepare("SELECT COUNT(*) as n FROM audit_log WHERE $where");
        $stmt->execute($params);
        $totalEntries = (int)$stmt->fetch()['n'];
        $totalPages   = max(1, (int)ceil($totalEntries / $limit));

        $stmt = $db->prepare(
            "SELECT id, timestamp, user_id, action, resource_type, resource_id,
                    ip_address, success, error_message, metadata
             FROM audit_log WHERE $where
             ORDER BY timestamp DESC LIMIT ? OFFSET ?"
        );
        foreach ($params as $i => $v) {
            $stmt->bindValue($i + 1, $v);
        }
        $stmt->bindValue(count($params) + 1, $limit, PDO::PARAM_INT);
        $stmt->bindValue(count($params) + 2, $offset, PDO::PARAM_INT);
        $stmt->execute();
        $entries = $stmt->fetchAll();

        // Distinct values for filter dropdowns
        $actionValues   = array_column(
            $db->query("SELECT DISTINCT action FROM audit_log ORDER BY action")->fetchAll(), 'action');
        $resourceValues = array_column(
            $db->query("SELECT DISTINCT resource_type FROM audit_log ORDER BY resource_type")->fetchAll(), 'resource_type');
    } else {
        $error = 'audit_log table not found — enable audit logging in the tracker config.';
    }
} catch (Exception $e) {
    $error = $e->getMessage();
}

function buildPageUrl(array $overrides): string {
    $base = array_merge($_GET, $overrides);
    return '?' . http_build_query(array_filter($base, fn($v) => $v !== ''));
}

include 'includes/header.php';
?>

<!-- Filters -->
<div class="bg-gray-800 border border-gray-700 rounded-lg p-4 mb-6">
    <form method="GET" class="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-3">
        <div>
            <label class="block text-xs text-gray-400 mb-1">Action</label>
            <select name="action" class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-white text-sm focus:outline-none focus:ring-2 focus:ring-blue-500">
                <option value="">All actions</option>
                <?php foreach ($actionValues as $a): ?>
                <option value="<?= htmlspecialchars($a) ?>" <?= $filterAction === $a ? 'selected' : '' ?>><?= htmlspecialchars($a) ?></option>
                <?php endforeach; ?>
            </select>
        </div>
        <div>
            <label class="block text-xs text-gray-400 mb-1">Resource</label>
            <select name="resource_type" class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-white text-sm focus:outline-none focus:ring-2 focus:ring-blue-500">
                <option value="">All resources</option>
                <?php foreach ($resourceValues as $r): ?>
                <option value="<?= htmlspecialchars($r) ?>" <?= $filterResource === $r ? 'selected' : '' ?>><?= htmlspecialchars($r) ?></option>
                <?php endforeach; ?>
            </select>
        </div>
        <div>
            <label class="block text-xs text-gray-400 mb-1">Result</label>
            <select name="success" class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-white text-sm focus:outline-none focus:ring-2 focus:ring-blue-500">
                <option value="" <?= $filterSuccess === '' ? 'selected' : '' ?>>All</option>
                <option value="1" <?= $filterSuccess === '1' ? 'selected' : '' ?>>Success</option>
                <option value="0" <?= $filterSuccess === '0' ? 'selected' : '' ?>>Failure</option>
            </select>
        </div>
        <div>
            <label class="block text-xs text-gray-400 mb-1">Window (days)</label>
            <select name="days" class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-white text-sm focus:outline-none focus:ring-2 focus:ring-blue-500">
                <?php foreach ([1, 3, 7, 14, 30, 90] as $d): ?>
                <option value="<?= $d ?>" <?= $filterDays === $d ? 'selected' : '' ?>><?= $d ?> day<?= $d > 1 ? 's' : '' ?></option>
                <?php endforeach; ?>
            </select>
        </div>
        <div class="flex items-end">
            <button type="submit" class="w-full px-4 py-2 bg-blue-600 text-white rounded text-sm hover:bg-blue-700">
                <i data-lucide="filter" class="inline w-4 h-4 mr-1"></i>Apply
            </button>
        </div>
    </form>
</div>

<!-- Summary bar -->
<div class="flex items-center justify-between mb-4">
    <p class="text-sm text-gray-400">
        <?= number_format($totalEntries) ?> entries in the last <?= $filterDays ?> day<?= $filterDays > 1 ? 's' : '' ?>
        <?= $filterAction ? " · action=<strong class='text-white'>$filterAction</strong>" : '' ?>
        <?= $filterResource ? " · resource=<strong class='text-white'>$filterResource</strong>" : '' ?>
    </p>
    <?php if ($totalEntries > 0): ?>
    <p class="text-sm text-gray-400">Page <?= $page ?> of <?= $totalPages ?></p>
    <?php endif; ?>
</div>

<?php if ($error): ?>
<div class="bg-red-900 border border-red-700 rounded-lg p-4 mb-6">
    <p class="text-sm text-red-200"><i data-lucide="alert-triangle" class="inline w-4 h-4 mr-1"></i><?= htmlspecialchars($error) ?></p>
</div>
<?php endif; ?>

<!-- Audit Table -->
<div class="bg-gray-800 border border-gray-700 rounded-lg overflow-hidden">
    <div class="overflow-x-auto">
        <table class="min-w-full divide-y divide-gray-700 text-sm">
            <thead class="bg-gray-900">
                <tr>
                    <th class="px-4 py-3 text-left text-xs text-gray-400 uppercase">Time</th>
                    <th class="px-4 py-3 text-left text-xs text-gray-400 uppercase">Action</th>
                    <th class="px-4 py-3 text-left text-xs text-gray-400 uppercase">Resource</th>
                    <th class="px-4 py-3 text-left text-xs text-gray-400 uppercase">ID</th>
                    <th class="px-4 py-3 text-left text-xs text-gray-400 uppercase">User</th>
                    <th class="px-4 py-3 text-left text-xs text-gray-400 uppercase">IP</th>
                    <th class="px-4 py-3 text-left text-xs text-gray-400 uppercase">Result</th>
                    <th class="px-4 py-3 text-left text-xs text-gray-400 uppercase">Detail</th>
                </tr>
            </thead>
            <tbody class="divide-y divide-gray-700">
                <?php if (empty($entries)): ?>
                <tr>
                    <td colspan="8" class="px-4 py-8 text-center text-gray-500">
                        <i data-lucide="clipboard-list" class="w-8 h-8 mx-auto mb-2 opacity-40"></i>
                        <p>No audit entries found</p>
                    </td>
                </tr>
                <?php else: foreach ($entries as $e):
                    $meta = json_decode($e['metadata'] ?? '{}', true) ?? [];
                    $isSuccess = (bool)$e['success'];
                ?>
                <tr class="hover:bg-gray-750" x-data="{ open: false }">
                    <td class="px-4 py-2 whitespace-nowrap text-gray-400 font-mono text-xs" title="<?= date('Y-m-d H:i:s', $e['timestamp']) ?>">
                        <?= timeAgo($e['timestamp']) ?>
                    </td>
                    <td class="px-4 py-2 whitespace-nowrap">
                        <span class="px-2 py-0.5 rounded text-xs font-medium bg-indigo-900 text-indigo-300">
                            <?= htmlspecialchars($e['action']) ?>
                        </span>
                    </td>
                    <td class="px-4 py-2 whitespace-nowrap text-blue-400 text-xs"><?= htmlspecialchars($e['resource_type']) ?></td>
                    <td class="px-4 py-2 whitespace-nowrap font-mono text-xs text-gray-300"><?= htmlspecialchars((string)($e['resource_id'] ?? '—')) ?></td>
                    <td class="px-4 py-2 whitespace-nowrap text-xs text-purple-400"><?= $e['user_id'] !== null ? htmlspecialchars($e['user_id']) : '—' ?></td>
                    <td class="px-4 py-2 whitespace-nowrap font-mono text-xs text-gray-400"><?= htmlspecialchars($e['ip_address'] ?? '—') ?></td>
                    <td class="px-4 py-2 whitespace-nowrap">
                        <?php if ($isSuccess): ?>
                        <span class="flex items-center gap-1 text-green-400 text-xs">
                            <i data-lucide="check" class="w-3.5 h-3.5"></i>OK
                        </span>
                        <?php else: ?>
                        <span class="flex items-center gap-1 text-red-400 text-xs">
                            <i data-lucide="x" class="w-3.5 h-3.5"></i>FAIL
                        </span>
                        <?php endif; ?>
                    </td>
                    <td class="px-4 py-2 text-xs text-gray-500 max-w-xs">
                        <?php if ($e['error_message']): ?>
                        <span class="text-red-400"><?= htmlspecialchars(mb_strimwidth($e['error_message'], 0, 60, '…')) ?></span>
                        <?php elseif (!empty($meta)): ?>
                        <button @click="open = !open" class="text-gray-500 hover:text-gray-300 underline">meta</button>
                        <div x-show="open" x-cloak class="mt-1 p-2 bg-gray-900 rounded font-mono text-xs text-gray-300 whitespace-pre-wrap">
                            <?= htmlspecialchars(json_encode($meta, JSON_PRETTY_PRINT)) ?>
                        </div>
                        <?php endif; ?>
                    </td>
                </tr>
                <?php endforeach; endif; ?>
            </tbody>
        </table>
    </div>
</div>

<!-- Pagination -->
<?php if ($totalPages > 1): ?>
<div class="flex justify-between items-center mt-4">
    <p class="text-sm text-gray-400">
        <?= number_format($offset + 1) ?>–<?= number_format(min($offset + $limit, $totalEntries)) ?> of <?= number_format($totalEntries) ?>
    </p>
    <div class="flex gap-2">
        <?php if ($page > 1): ?>
        <a href="<?= htmlspecialchars(buildPageUrl(['page' => $page - 1])) ?>"
           class="px-3 py-1.5 bg-gray-700 text-white rounded text-sm hover:bg-gray-600">← Prev</a>
        <?php endif; ?>
        <?php for ($i = max(1, $page - 2); $i <= min($totalPages, $page + 2); $i++): ?>
        <a href="<?= htmlspecialchars(buildPageUrl(['page' => $i])) ?>"
           class="px-3 py-1.5 <?= $i === $page ? 'bg-blue-600' : 'bg-gray-700 hover:bg-gray-600' ?> text-white rounded text-sm"><?= $i ?></a>
        <?php endfor; ?>
        <?php if ($page < $totalPages): ?>
        <a href="<?= htmlspecialchars(buildPageUrl(['page' => $page + 1])) ?>"
           class="px-3 py-1.5 bg-gray-700 text-white rounded text-sm hover:bg-gray-600">Next →</a>
        <?php endif; ?>
    </div>
</div>
<?php endif; ?>

<?php include 'includes/footer.php'; ?>
