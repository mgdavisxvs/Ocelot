<?php
require_once 'config.php';
requireAuth();

$pageTitle = 'Torrent Management';
$success = '';
$error = '';

// Handle torrent actions
if ($_SERVER['REQUEST_METHOD'] === 'POST') {
    $action = $_POST['action'] ?? '';

    try {
        if ($action === 'add') {
            $torrentID = (int)$_POST['torrent_id'];
            $infoHash = $_POST['info_hash'];

            if (strlen($infoHash) !== 20 && strlen($infoHash) !== 40) {
                throw new Exception('Info hash must be 20 or 40 characters');
            }

            TrackerAPI::addTorrent($torrentID, $infoHash);
            $success = "Torrent added successfully";
        }
    } catch (Exception $e) {
        $error = $e->getMessage();
    }
}

// Fetch torrents from database
try {
    $db = OcelotDB::connect();

    // Get active torrents with peer counts
    $torrents = $db->query("
        SELECT torrent_id,
               COUNT(*) as total_peers,
               SUM(CASE WHEN torrent_left = 0 THEN 1 ELSE 0 END) as seeders,
               SUM(CASE WHEN torrent_left > 0 THEN 1 ELSE 0 END) as leechers,
               MAX(timestamp) as last_announce
        FROM peers
        WHERE timestamp > " . (time() - 7200) . "
        GROUP BY torrent_id
        ORDER BY total_peers DESC
        LIMIT 100
    ")->fetchAll();

    // Get snatch counts
    $snatchCounts = [];
    $snatches = $db->query("
        SELECT torrent_id, COUNT(*) as snatch_count
        FROM snatches
        GROUP BY torrent_id
    ")->fetchAll();

    foreach ($snatches as $snatch) {
        $snatchCounts[$snatch['torrent_id']] = $snatch['snatch_count'];
    }

} catch (Exception $e) {
    $error = $e->getMessage();
    $torrents = [];
}

include 'includes/header.php';
?>

<div x-data="{ showAddModal: false }">
    <!-- Success/Error Messages -->
    <?php if ($success): ?>
    <div class="rounded-md bg-green-900 border border-green-700 p-4 mb-6">
        <div class="flex">
            <i data-lucide="check-circle" class="h-5 w-5 text-green-400"></i>
            <div class="ml-3">
                <p class="text-sm text-green-200"><?= htmlspecialchars($success) ?></p>
            </div>
        </div>
    </div>
    <?php endif; ?>

    <?php if ($error): ?>
    <div class="rounded-md bg-red-900 border border-red-700 p-4 mb-6">
        <div class="flex">
            <i data-lucide="alert-circle" class="h-5 w-5 text-red-400"></i>
            <div class="ml-3">
                <p class="text-sm text-red-200"><?= htmlspecialchars($error) ?></p>
            </div>
        </div>
    </div>
    <?php endif; ?>

    <!-- Action Bar -->
    <div class="mb-6 flex justify-between items-center">
        <div>
            <p class="text-sm text-gray-400">
                <i data-lucide="info" class="inline-block w-4 h-4"></i>
                Showing <?= count($torrents) ?> active torrents
            </p>
        </div>
        <button @click="showAddModal = true"
                class="inline-flex items-center px-4 py-2 border border-transparent text-sm font-medium rounded-md text-white bg-blue-600 hover:bg-blue-700">
            <i data-lucide="plus" class="w-4 h-4 mr-2"></i>
            Add Torrent
        </button>
    </div>

    <!-- Torrents Table -->
    <div class="bg-gray-800 shadow rounded-lg border border-gray-700 overflow-hidden">
        <table class="min-w-full divide-y divide-gray-700">
            <thead class="bg-gray-900">
                <tr>
                    <th class="px-6 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">Torrent ID</th>
                    <th class="px-6 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">Seeders</th>
                    <th class="px-6 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">Leechers</th>
                    <th class="px-6 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">Peers</th>
                    <th class="px-6 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">Snatches</th>
                    <th class="px-6 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">Last Announce</th>
                    <th class="px-6 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">Health</th>
                </tr>
            </thead>
            <tbody class="divide-y divide-gray-700">
                <?php if (empty($torrents)): ?>
                <tr>
                    <td colspan="7" class="px-6 py-8 text-center text-gray-400">
                        <i data-lucide="disc" class="w-12 h-12 mx-auto mb-2 opacity-50"></i>
                        <p>No active torrents found</p>
                    </td>
                </tr>
                <?php else: ?>
                    <?php foreach ($torrents as $torrent): ?>
                    <?php
                    $healthPercent = $torrent['total_peers'] > 0
                        ? ($torrent['seeders'] / $torrent['total_peers']) * 100
                        : 0;
                    $healthColor = $healthPercent >= 50 ? 'bg-green-500' : ($healthPercent >= 20 ? 'bg-yellow-500' : 'bg-red-500');
                    $snatches = $snatchCounts[$torrent['torrent_id']] ?? 0;
                    ?>
                    <tr>
                        <td class="px-6 py-4 whitespace-nowrap text-sm font-medium text-white">
                            <?= $torrent['torrent_id'] ?>
                        </td>
                        <td class="px-6 py-4 whitespace-nowrap text-sm text-green-400">
                            <i data-lucide="arrow-up" class="inline w-4 h-4"></i>
                            <?= $torrent['seeders'] ?>
                        </td>
                        <td class="px-6 py-4 whitespace-nowrap text-sm text-yellow-400">
                            <i data-lucide="arrow-down" class="inline w-4 h-4"></i>
                            <?= $torrent['leechers'] ?>
                        </td>
                        <td class="px-6 py-4 whitespace-nowrap text-sm text-blue-400">
                            <?= $torrent['total_peers'] ?>
                        </td>
                        <td class="px-6 py-4 whitespace-nowrap text-sm text-purple-400">
                            <?= $snatches ?>
                        </td>
                        <td class="px-6 py-4 whitespace-nowrap text-sm text-gray-400">
                            <?= timeAgo($torrent['last_announce']) ?>
                        </td>
                        <td class="px-6 py-4 whitespace-nowrap">
                            <div class="flex items-center">
                                <div class="w-16 bg-gray-700 rounded-full h-2 mr-2">
                                    <div class="<?= $healthColor ?> h-2 rounded-full" style="width: <?= $healthPercent ?>%"></div>
                                </div>
                                <span class="text-xs text-gray-400"><?= number_format($healthPercent, 0) ?>%</span>
                            </div>
                        </td>
                    </tr>
                    <?php endforeach; ?>
                <?php endif; ?>
            </tbody>
        </table>
    </div>

    <!-- Add Torrent Modal -->
    <div x-show="showAddModal" x-cloak
         class="fixed inset-0 bg-gray-900 bg-opacity-75 overflow-y-auto h-full w-full z-50"
         @click.self="showAddModal = false"
         x-data="torrentUploader()">
        <div class="relative top-20 mx-auto p-5 border w-96 shadow-lg rounded-md bg-gray-800 border-gray-700">
            <div class="flex justify-between items-center mb-4">
                <h3 class="text-lg font-medium text-white">Add New Torrent</h3>
                <button @click="showAddModal = false" class="text-gray-400 hover:text-white">
                    <i data-lucide="x" class="w-5 h-5"></i>
                </button>
            </div>

            <!-- Drag & Drop Zone -->
            <div
                @drop.prevent="handleDrop($event)"
                @dragover.prevent="dragover = true"
                @dragleave.prevent="dragover = false"
                :class="dragover ? 'border-blue-500 bg-blue-900 bg-opacity-20' : 'border-gray-600'"
                class="mb-4 border-2 border-dashed rounded-lg p-6 text-center cursor-pointer transition-colors"
                @click="$refs.fileInput.click()">

                <input type="file"
                       x-ref="fileInput"
                       @change="handleFileSelect($event)"
                       accept=".torrent"
                       class="hidden">

                <div x-show="!uploading && !torrentInfo">
                    <i data-lucide="upload" class="w-12 h-12 mx-auto mb-2 text-gray-400"></i>
                    <p class="text-sm text-gray-300">Drop .torrent file here</p>
                    <p class="text-xs text-gray-500 mt-1">or click to browse</p>
                </div>

                <div x-show="uploading" class="text-blue-400">
                    <svg class="animate-spin h-8 w-8 mx-auto mb-2" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
                        <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
                        <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
                    </svg>
                    <p class="text-sm">Parsing torrent...</p>
                </div>

                <div x-show="torrentInfo" class="text-left">
                    <div class="flex items-start justify-between mb-2">
                        <div class="flex-1">
                            <p class="text-sm font-medium text-white truncate" x-text="torrentInfo?.name"></p>
                            <p class="text-xs text-gray-400" x-text="torrentInfo?.size_formatted"></p>
                        </div>
                        <button @click.stop="clearTorrent()" class="text-gray-400 hover:text-white ml-2">
                            <i data-lucide="x" class="w-4 h-4"></i>
                        </button>
                    </div>
                    <div class="text-xs text-gray-500">
                        <span x-text="torrentInfo?.files + ' file(s)'"></span> •
                        <span x-text="torrentInfo?.pieces + ' pieces'"></span>
                    </div>
                </div>
            </div>

            <!-- Error Message -->
            <div x-show="errorMsg" class="mb-4 p-3 bg-red-900 border border-red-700 rounded-md">
                <p class="text-sm text-red-200" x-text="errorMsg"></p>
            </div>

            <form method="POST" class="space-y-4">
                <input type="hidden" name="action" value="add">

                <div>
                    <label class="block text-sm font-medium text-gray-300 mb-1">Torrent ID</label>
                    <input type="number"
                           name="torrent_id"
                           x-model="torrentId"
                           required
                           class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500">
                </div>

                <div>
                    <label class="block text-sm font-medium text-gray-300 mb-1">Info Hash</label>
                    <input type="text"
                           name="info_hash"
                           x-model="infoHash"
                           required
                           :readonly="torrentInfo !== null"
                           placeholder="20 or 40 character hash"
                           class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
                           :class="torrentInfo ? 'bg-gray-900' : 'bg-gray-700'">
                    <p class="text-xs text-gray-400 mt-1">
                        <span x-show="!torrentInfo">Hex-encoded SHA-1 hash or upload .torrent file</span>
                        <span x-show="torrentInfo" class="text-green-400">✓ Auto-filled from .torrent file</span>
                    </p>
                </div>

                <div class="flex gap-2 pt-4">
                    <button type="submit"
                            :disabled="!infoHash"
                            class="flex-1 px-4 py-2 bg-blue-600 text-white rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed">
                        Add Torrent
                    </button>
                    <button type="button" @click="showAddModal = false"
                            class="flex-1 px-4 py-2 bg-gray-700 text-white rounded-md hover:bg-gray-600">
                        Cancel
                    </button>
                </div>
            </form>
        </div>
    </div>

<script>
function torrentUploader() {
    return {
        dragover: false,
        uploading: false,
        torrentInfo: null,
        errorMsg: null,
        torrentId: '',
        infoHash: '',

        handleDrop(e) {
            this.dragover = false;
            const files = e.dataTransfer.files;
            if (files.length > 0) {
                this.uploadTorrent(files[0]);
            }
        },

        handleFileSelect(e) {
            const files = e.target.files;
            if (files.length > 0) {
                this.uploadTorrent(files[0]);
            }
        },

        async uploadTorrent(file) {
            if (!file.name.endsWith('.torrent')) {
                this.errorMsg = 'Please upload a .torrent file';
                return;
            }

            this.uploading = true;
            this.errorMsg = null;

            const formData = new FormData();
            formData.append('torrent', file);

            try {
                const response = await fetch('api/parse-torrent.php', {
                    method: 'POST',
                    body: formData
                });

                const data = await response.json();

                if (!response.ok || data.error) {
                    throw new Error(data.error || 'Failed to parse torrent');
                }

                this.torrentInfo = data;
                this.infoHash = data.info_hash;

                // Reinitialize Lucide icons for the new content
                setTimeout(() => lucide.createIcons(), 100);

            } catch (error) {
                this.errorMsg = error.message;
                console.error('Upload error:', error);
            } finally {
                this.uploading = false;
            }
        },

        clearTorrent() {
            this.torrentInfo = null;
            this.infoHash = '';
            this.errorMsg = null;
            this.$refs.fileInput.value = '';
        }
    };
}
</script>
</div>

<?php include 'includes/footer.php'; ?>
