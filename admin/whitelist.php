<?php
require_once 'config.php';
requireAuth();

$pageTitle = 'Whitelist Management';
$success = '';
$error   = '';

// Handle add/remove
if ($_SERVER['REQUEST_METHOD'] === 'POST') {
    $action = $_POST['action'] ?? '';
    $prefix = trim($_POST['prefix'] ?? '');

    if ($prefix === '') {
        $error = 'Prefix cannot be empty.';
    } else {
        try {
            if ($action === 'add') {
                TrackerAPI::addWhitelist($prefix);
                $success = "Prefix " . htmlspecialchars($prefix) . " added to whitelist.";
            } elseif ($action === 'remove') {
                TrackerAPI::removeWhitelist($prefix);
                $success = "Prefix " . htmlspecialchars($prefix) . " removed from whitelist.";
            } else {
                $error = 'Unknown action.';
            }
        } catch (Exception $e) {
            $error = $e->getMessage();
        }
    }
}

// Fetch current whitelist from tracker
$whitelist = [];
try {
    $data = TrackerAPI::getWhitelist();
    if (is_array($data)) {
        $whitelist = $data['prefixes'] ?? $data['whitelist'] ?? $data;
        if (!is_array($whitelist)) $whitelist = [];
    }
} catch (Exception $e) {
    $error = $error ?: $e->getMessage();
}

// Known common BitTorrent client peer-id prefixes for reference
$knownClients = [
    '-qB' => 'qBittorrent',   '-DE' => 'Deluge',       '-TR' => 'Transmission',
    '-lt' => 'libtorrent',    '-UT' => 'µTorrent',     '-BT' => 'BitTorrent',
    '-az' => 'Azureus/Vuze', '-RT' => 'rTorrent',     '-LP' => 'libtorrent-python',
    '-WW' => 'WebTorrent',   '-ML' => 'MLDonkey',     '-TN' => 'TorrentStorm',
];

include 'includes/header.php';
?>

<!-- Messages -->
<?php if ($success): ?>
<div class="bg-green-900 border border-green-700 rounded-lg p-4 mb-6 flex items-center gap-3">
    <i data-lucide="check-circle" class="w-5 h-5 text-green-400 flex-shrink-0"></i>
    <p class="text-sm text-green-200"><?= $success ?></p>
</div>
<?php endif; ?>
<?php if ($error): ?>
<div class="bg-red-900 border border-red-700 rounded-lg p-4 mb-6 flex items-center gap-3">
    <i data-lucide="alert-circle" class="w-5 h-5 text-red-400 flex-shrink-0"></i>
    <p class="text-sm text-red-200"><?= htmlspecialchars($error) ?></p>
</div>
<?php endif; ?>

<div class="grid grid-cols-1 lg:grid-cols-3 gap-6">

    <!-- Current Whitelist -->
    <div class="lg:col-span-2 bg-gray-800 border border-gray-700 rounded-lg overflow-hidden">
        <div class="px-5 py-4 border-b border-gray-700 flex items-center gap-2">
            <i data-lucide="shield-check" class="w-5 h-5 text-green-400"></i>
            <h2 class="text-lg font-semibold text-white">Active Prefixes</h2>
            <span class="ml-auto text-xs text-gray-500"><?= count($whitelist) ?> entries</span>
        </div>
        <div class="overflow-x-auto">
            <table class="min-w-full divide-y divide-gray-700 text-sm">
                <thead class="bg-gray-900">
                    <tr>
                        <th class="px-5 py-3 text-left text-xs text-gray-400 uppercase">#</th>
                        <th class="px-5 py-3 text-left text-xs text-gray-400 uppercase">Prefix</th>
                        <th class="px-5 py-3 text-left text-xs text-gray-400 uppercase">Client</th>
                        <th class="px-5 py-3 text-right text-xs text-gray-400 uppercase">Action</th>
                    </tr>
                </thead>
                <tbody class="divide-y divide-gray-700">
                    <?php if (empty($whitelist)): ?>
                    <tr>
                        <td colspan="4" class="px-5 py-6 text-center text-gray-500">
                            <i data-lucide="list-x" class="w-8 h-8 mx-auto mb-2 opacity-40"></i>
                            <?php if ($error): ?>
                            <p>Could not reach tracker API</p>
                            <?php else: ?>
                            <p>Whitelist is empty — all clients will be rejected</p>
                            <?php endif; ?>
                        </td>
                    </tr>
                    <?php else:
                        foreach ($whitelist as $i => $prefix):
                            $known = null;
                            foreach ($knownClients as $p => $name) {
                                if (str_starts_with($prefix, $p)) { $known = $name; break; }
                            }
                    ?>
                    <tr class="hover:bg-gray-750">
                        <td class="px-5 py-3 text-gray-500 text-xs font-mono"><?= $i + 1 ?></td>
                        <td class="px-5 py-3 font-mono text-white"><?= htmlspecialchars($prefix) ?></td>
                        <td class="px-5 py-3 text-gray-400 text-xs"><?= $known ?? '<span class="text-gray-600">unknown</span>' ?></td>
                        <td class="px-5 py-3 text-right">
                            <form method="POST" class="inline" onsubmit="return confirm('Remove <?= htmlspecialchars(addslashes($prefix)) ?>?')">
                                <input type="hidden" name="action" value="remove">
                                <input type="hidden" name="prefix" value="<?= htmlspecialchars($prefix) ?>">
                                <button type="submit" class="text-red-400 hover:text-red-300 text-xs flex items-center gap-1 ml-auto">
                                    <i data-lucide="trash-2" class="w-3.5 h-3.5"></i>Remove
                                </button>
                            </form>
                        </td>
                    </tr>
                    <?php endforeach; endif; ?>
                </tbody>
            </table>
        </div>
    </div>

    <!-- Add + Reference panel -->
    <div class="space-y-6">

        <!-- Add Prefix -->
        <div class="bg-gray-800 border border-gray-700 rounded-lg p-5">
            <h2 class="text-base font-semibold text-white mb-4 flex items-center gap-2">
                <i data-lucide="plus-circle" class="w-4 h-4 text-blue-400"></i>
                Add Prefix
            </h2>
            <form method="POST" class="space-y-3">
                <input type="hidden" name="action" value="add">
                <div>
                    <label class="block text-xs text-gray-400 mb-1">Peer-ID Prefix</label>
                    <input type="text" name="prefix" required maxlength="20"
                           placeholder="-qB4400-"
                           class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-white text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500">
                    <p class="text-xs text-gray-500 mt-1">Azureus-style: -XX####- or Shadow-style first character</p>
                </div>
                <button type="submit" class="w-full px-4 py-2 bg-blue-600 text-white rounded text-sm hover:bg-blue-700">
                    Add to Whitelist
                </button>
            </form>
        </div>

        <!-- Quick-add known clients -->
        <div class="bg-gray-800 border border-gray-700 rounded-lg p-5">
            <h2 class="text-base font-semibold text-white mb-3 flex items-center gap-2">
                <i data-lucide="zap" class="w-4 h-4 text-yellow-400"></i>
                Quick Add
            </h2>
            <div class="space-y-2">
                <?php foreach ($knownClients as $prefix => $name): ?>
                <form method="POST" class="flex items-center justify-between">
                    <input type="hidden" name="action" value="add">
                    <input type="hidden" name="prefix" value="<?= htmlspecialchars($prefix) ?>">
                    <span class="text-sm text-gray-300">
                        <span class="font-mono text-xs text-blue-400"><?= htmlspecialchars($prefix) ?></span>
                        <span class="text-gray-500 ml-1"><?= htmlspecialchars($name) ?></span>
                    </span>
                    <button type="submit" class="text-xs px-2 py-1 bg-gray-700 text-gray-300 rounded hover:bg-gray-600">Add</button>
                </form>
                <?php endforeach; ?>
            </div>
        </div>

    </div>
</div>

<?php include 'includes/footer.php'; ?>
