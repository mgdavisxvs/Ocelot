<?php
require_once 'config.php';
requireAuth();

$pageTitle = 'Torrent Management';
$success = '';
$error   = '';

// Handle torrent actions
if ($_SERVER['REQUEST_METHOD'] === 'POST') {
    $action = $_POST['action'] ?? '';

    try {
        if ($action === 'add') {
            $torrentID = (int)$_POST['torrent_id'];
            $infoHash  = trim($_POST['info_hash'] ?? '');

            if ($torrentID <= 0) {
                throw new Exception('Torrent ID must be a positive integer.');
            }
            if (strlen($infoHash) !== 20 && strlen($infoHash) !== 40) {
                throw new Exception('Info hash must be 20 bytes (binary) or 40 hex characters.');
            }

            TrackerAPI::addTorrent($torrentID, $infoHash);
            $success = "Torrent $torrentID registered.";

        } elseif ($action === 'delete') {
            $infoHash = trim($_POST['info_hash'] ?? '');
            if (strlen($infoHash) !== 20 && strlen($infoHash) !== 40) {
                throw new Exception('Invalid info hash.');
            }
            TrackerAPI::deleteTorrent($infoHash);
            $success = 'Torrent removed from tracker.';

        } elseif ($action === 'freeleech') {
            $infoHash = trim($_POST['info_hash'] ?? '');
            $freeType = (int)($_POST['free_type'] ?? 0);
            if (!in_array($freeType, [0, 1, 2], true)) {
                throw new Exception('Invalid freeleech type.');
            }
            TrackerAPI::changeFreeleech($infoHash, $freeType);
            $labels = ['normal', 'free', 'neutral'];
            $success = "Freeleech set to {$labels[$freeType]}.";

        } else {
            throw new Exception('Unknown action.');
        }

    } catch (Exception $e) {
        $error = $e->getMessage();
    }
}

// Fetch all registered torrents from torrent_hashes, join with peer activity
$torrents     = [];
$snatchCounts = [];
try {
    $db     = OcelotDB::connect();
    $cutoff = time() - 7200;

    $stmt = $db->prepare("
        SELECT th.torrent_id,
               th.info_hash,
               COALESCE(p.total_peers, 0) as total_peers,
               COALESCE(p.seeders,     0) as seeders,
               COALESCE(p.leechers,    0) as leechers,
               p.last_announce
        FROM torrent_hashes th
        LEFT JOIN (
            SELECT torrent_id,
                   COUNT(*) as total_peers,
                   SUM(CASE WHEN torrent_left = 0 THEN 1 ELSE 0 END) as seeders,
                   SUM(CASE WHEN torrent_left > 0 THEN 1 ELSE 0 END) as leechers,
                   MAX(timestamp) as last_announce
            FROM peers
            WHERE timestamp > :cutoff
            GROUP BY torrent_id
        ) p ON p.torrent_id = th.torrent_id
        ORDER BY total_peers DESC, th.torrent_id
        LIMIT 500
    ");
    $stmt->execute([':cutoff' => $cutoff]);
    $torrents = $stmt->fetchAll();

    // Snatch counts
    $snatches = $db->query("
        SELECT torrent_id, COUNT(*) as snatch_count
        FROM snatches GROUP BY torrent_id
    ")->fetchAll();
    foreach ($snatches as $s) {
        $snatchCounts[$s['torrent_id']] = $s['snatch_count'];
    }

} catch (Exception $e) {
    $error = $error ?: $e->getMessage();
}

include 'includes/header.php';
?>

<div x-data="torrentMgmt()">
    <!-- Messages -->
    <?php if ($success): ?>
    <div class="bg-green-900 border border-green-700 rounded-lg p-4 mb-6 flex items-center gap-3">
        <i data-lucide="check-circle" class="w-5 h-5 text-green-400 flex-shrink-0"></i>
        <p class="text-sm text-green-200"><?= htmlspecialchars($success) ?></p>
    </div>
    <?php endif; ?>
    <?php if ($error): ?>
    <div class="bg-red-900 border border-red-700 rounded-lg p-4 mb-6 flex items-center gap-3">
        <i data-lucide="alert-circle" class="w-5 h-5 text-red-400 flex-shrink-0"></i>
        <p class="text-sm text-red-200"><?= htmlspecialchars($error) ?></p>
    </div>
    <?php endif; ?>

    <!-- Action Bar -->
    <div class="mb-4 flex flex-wrap gap-3 items-center">
        <div class="relative flex-1 min-w-[200px]">
            <i data-lucide="search" class="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-gray-500 pointer-events-none"></i>
            <input type="text" x-model="search"
                   @input="filterRows($event.target.value)"
                   placeholder="Filter by torrent ID or info hash…"
                   class="w-full pl-9 pr-3 py-2 bg-gray-700 border border-gray-600 rounded text-white text-sm focus:outline-none focus:ring-2 focus:ring-blue-500">
        </div>
        <p class="text-sm text-gray-400 whitespace-nowrap" id="torrentCount">
            <?= number_format(count($torrents)) ?> torrents
            <span class="text-gray-600">· peers: last 2 h</span>
        </p>
        <button @click="showAdd = true"
                class="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-md text-sm hover:bg-blue-700">
            <i data-lucide="plus" class="w-4 h-4"></i>Add Torrent
        </button>
    </div>

    <!-- Torrents Table -->
    <div class="bg-gray-800 border border-gray-700 rounded-lg overflow-hidden">
        <div class="overflow-x-auto">
            <table class="min-w-full divide-y divide-gray-700 text-sm">
                <thead class="bg-gray-900">
                    <tr>
                        <th class="px-5 py-3 text-left text-xs text-gray-400 uppercase">ID</th>
                        <th class="px-5 py-3 text-left text-xs text-gray-400 uppercase">Info Hash</th>
                        <th class="px-5 py-3 text-left text-xs text-gray-400 uppercase">Seeders</th>
                        <th class="px-5 py-3 text-left text-xs text-gray-400 uppercase">Leechers</th>
                        <th class="px-5 py-3 text-left text-xs text-gray-400 uppercase">Snatches</th>
                        <th class="px-5 py-3 text-left text-xs text-gray-400 uppercase">Last Announce</th>
                        <th class="px-5 py-3 text-left text-xs text-gray-400 uppercase">Health</th>
                        <th class="px-5 py-3 text-right text-xs text-gray-400 uppercase">Actions</th>
                    </tr>
                </thead>
                <tbody class="divide-y divide-gray-700" id="torrentTableBody">
                    <?php if (empty($torrents)): ?>
                    <tr>
                        <td colspan="8" class="px-5 py-8 text-center text-gray-500">
                            <i data-lucide="disc" class="w-8 h-8 mx-auto mb-2 opacity-40"></i>
                            <p>No torrents registered</p>
                        </td>
                    </tr>
                    <?php else: foreach ($torrents as $t):
                        $snatches   = $snatchCounts[$t['torrent_id']] ?? 0;
                        $healthPct  = $t['total_peers'] > 0 ? ($t['seeders'] / $t['total_peers']) * 100 : 0;
                        $healthColor = $healthPct >= 50 ? 'bg-green-500' : ($healthPct >= 20 ? 'bg-yellow-500' : 'bg-red-500');
                        $hashDisplay = strlen($t['info_hash']) === 40
                            ? substr($t['info_hash'], 0, 8) . '…' . substr($t['info_hash'], -4)
                            : bin2hex(substr($t['info_hash'], 0, 4)) . '…';
                    ?>
                    <tr class="hover:bg-gray-750 torrent-row"
                        data-tid="<?= $t['torrent_id'] ?>"
                        data-hash="<?= htmlspecialchars($t['info_hash']) ?>">
                        <td class="px-5 py-3 font-mono text-white"><?= $t['torrent_id'] ?></td>
                        <td class="px-5 py-3 font-mono text-xs text-gray-400" title="<?= htmlspecialchars($t['info_hash']) ?>">
                            <?= htmlspecialchars($hashDisplay) ?>
                        </td>
                        <td class="px-5 py-3 text-green-400">
                            <span class="flex items-center gap-1">
                                <i data-lucide="arrow-up" class="w-3.5 h-3.5"></i><?= $t['seeders'] ?>
                            </span>
                        </td>
                        <td class="px-5 py-3 text-yellow-400">
                            <span class="flex items-center gap-1">
                                <i data-lucide="arrow-down" class="w-3.5 h-3.5"></i><?= $t['leechers'] ?>
                            </span>
                        </td>
                        <td class="px-5 py-3 text-purple-400"><?= number_format($snatches) ?></td>
                        <td class="px-5 py-3 text-gray-400 text-xs">
                            <?= $t['last_announce'] ? timeAgo($t['last_announce']) : '<span class="text-gray-600">—</span>' ?>
                        </td>
                        <td class="px-5 py-3">
                            <div class="flex items-center gap-2">
                                <div class="w-14 bg-gray-700 rounded-full h-1.5">
                                    <div class="<?= $healthColor ?> h-1.5 rounded-full" style="width: <?= $healthPct ?>%"></div>
                                </div>
                                <span class="text-xs text-gray-500"><?= number_format($healthPct, 0) ?>%</span>
                            </div>
                        </td>
                        <td class="px-5 py-3 text-right">
                            <div class="flex items-center justify-end gap-3">
                                <button @click="openFreeleech(<?= htmlspecialchars(json_encode($t['info_hash'])) ?>, <?= $t['torrent_id'] ?>)"
                                        class="text-yellow-400 hover:text-yellow-300" title="Set freeleech">
                                    <i data-lucide="zap" class="w-4 h-4"></i>
                                </button>
                                <button @click="confirmDelete(<?= htmlspecialchars(json_encode($t['info_hash'])) ?>, <?= $t['torrent_id'] ?>)"
                                        class="text-red-400 hover:text-red-300" title="Delete">
                                    <i data-lucide="trash-2" class="w-4 h-4"></i>
                                </button>
                            </div>
                        </td>
                    </tr>
                    <?php endforeach; endif; ?>
                    <tr id="torrentNoResults" class="hidden">
                        <td colspan="8" class="px-5 py-6 text-center text-gray-500">No torrents match the filter.</td>
                    </tr>
                </tbody>
            </table>
        </div>
    </div>

    <!-- Add Torrent Modal -->
    <div x-show="showAdd" x-cloak
         class="fixed inset-0 bg-black/70 flex items-center justify-center z-50"
         @click.self="showAdd = false">
        <div class="bg-gray-800 border border-gray-700 rounded-lg p-6 w-[420px] shadow-xl">
            <div class="flex justify-between items-center mb-5">
                <h3 class="text-lg font-semibold text-white">Add Torrent</h3>
                <button @click="showAdd = false" class="text-gray-400 hover:text-white">
                    <i data-lucide="x" class="w-5 h-5"></i>
                </button>
            </div>

            <!-- Drag & Drop Zone -->
            <div @drop.prevent="handleDrop($event)"
                 @dragover.prevent="dragover = true"
                 @dragleave.prevent="dragover = false"
                 :class="dragover ? 'border-blue-500 bg-blue-900/20' : 'border-gray-600'"
                 class="mb-4 border-2 border-dashed rounded-lg p-5 text-center cursor-pointer transition-colors"
                 @click="$refs.fileInput.click()">
                <input type="file" x-ref="fileInput" @change="handleFileSelect($event)" accept=".torrent" class="hidden">
                <div x-show="!uploading && !torrentInfo">
                    <i data-lucide="upload" class="w-10 h-10 mx-auto mb-2 text-gray-500"></i>
                    <p class="text-sm text-gray-400">Drop .torrent file or click to browse</p>
                </div>
                <div x-show="uploading" class="text-blue-400">
                    <svg class="animate-spin h-7 w-7 mx-auto mb-2" fill="none" viewBox="0 0 24 24">
                        <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
                        <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
                    </svg>
                    <p class="text-sm">Parsing…</p>
                </div>
                <div x-show="torrentInfo" class="text-left">
                    <div class="flex items-start justify-between">
                        <div>
                            <p class="text-sm font-medium text-white truncate" x-text="torrentInfo?.name"></p>
                            <p class="text-xs text-gray-400" x-text="torrentInfo?.size_formatted + ' · ' + torrentInfo?.files + ' file(s)'"></p>
                        </div>
                        <button @click.stop="clearTorrent()" class="text-gray-400 hover:text-white ml-2">
                            <i data-lucide="x" class="w-4 h-4"></i>
                        </button>
                    </div>
                </div>
            </div>

            <div x-show="uploadError" class="mb-3 p-3 bg-red-900 border border-red-700 rounded text-sm text-red-200" x-text="uploadError"></div>

            <form method="POST" class="space-y-3">
                <input type="hidden" name="action" value="add">
                <div>
                    <label class="block text-xs text-gray-400 mb-1">Torrent ID</label>
                    <input type="number" name="torrent_id" required min="1"
                           class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-white text-sm focus:outline-none focus:ring-2 focus:ring-blue-500">
                </div>
                <div>
                    <label class="block text-xs text-gray-400 mb-1">Info Hash</label>
                    <input type="text" name="info_hash" x-model="addInfoHash" required
                           :readonly="torrentInfo !== null"
                           placeholder="40-char hex SHA-1"
                           :class="torrentInfo ? 'bg-gray-900 text-gray-400' : 'bg-gray-700'"
                           class="w-full px-3 py-2 border border-gray-600 rounded text-white text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500">
                    <p x-show="torrentInfo" class="text-xs text-green-400 mt-1">✓ Auto-filled from .torrent file</p>
                </div>
                <div class="flex gap-2 pt-1">
                    <button type="submit" :disabled="!addInfoHash"
                            class="flex-1 px-4 py-2 bg-blue-600 text-white rounded text-sm hover:bg-blue-700 disabled:opacity-50">
                        Register
                    </button>
                    <button type="button" @click="showAdd = false"
                            class="flex-1 px-4 py-2 bg-gray-700 text-white rounded text-sm hover:bg-gray-600">
                        Cancel
                    </button>
                </div>
            </form>
        </div>
    </div>

    <!-- Freeleech Modal -->
    <div x-show="showFreeleech" x-cloak
         class="fixed inset-0 bg-black/70 flex items-center justify-center z-50"
         @click.self="showFreeleech = false">
        <div class="bg-gray-800 border border-gray-700 rounded-lg p-6 w-80 shadow-xl">
            <h3 class="text-lg font-semibold text-white mb-1">Freeleech</h3>
            <p class="text-sm text-gray-400 mb-5">Torrent <span x-text="freeleechId" class="text-yellow-400 font-mono"></span></p>
            <form method="POST" class="space-y-4">
                <input type="hidden" name="action" value="freeleech">
                <input type="hidden" name="info_hash" :value="freeleechHash">
                <div class="space-y-2">
                    <?php foreach ([0 => 'Normal (billing on)', 1 => 'Free (no download credit)', 2 => 'Neutral (no upload credit)'] as $val => $label): ?>
                    <label class="flex items-center gap-3 p-3 bg-gray-700 rounded cursor-pointer hover:bg-gray-600">
                        <input type="radio" name="free_type" value="<?= $val ?>"
                               <?= $val === 0 ? 'checked' : '' ?>
                               class="text-blue-600">
                        <span class="text-sm text-gray-200"><?= $label ?></span>
                    </label>
                    <?php endforeach; ?>
                </div>
                <div class="flex gap-2 pt-1">
                    <button type="submit"
                            class="flex-1 px-4 py-2 bg-yellow-600 text-white rounded text-sm hover:bg-yellow-700">
                        Apply
                    </button>
                    <button type="button" @click="showFreeleech = false"
                            class="flex-1 px-4 py-2 bg-gray-700 text-white rounded text-sm hover:bg-gray-600">
                        Cancel
                    </button>
                </div>
            </form>
        </div>
    </div>

    <!-- Delete Confirm Modal -->
    <div x-show="showDelete" x-cloak
         class="fixed inset-0 bg-black/70 flex items-center justify-center z-50"
         @click.self="showDelete = false">
        <div class="bg-gray-800 border border-gray-700 rounded-lg p-6 w-80 shadow-xl">
            <h3 class="text-lg font-semibold text-white mb-2">Delete Torrent</h3>
            <p class="text-sm text-gray-300 mb-6">
                Remove torrent <span x-text="deleteId" class="text-red-400 font-mono font-bold"></span> from the tracker?
                Active peers will be disconnected.
            </p>
            <form method="POST" class="flex gap-2">
                <input type="hidden" name="action" value="delete">
                <input type="hidden" name="info_hash" :value="deleteHash">
                <button type="submit"
                        class="flex-1 px-4 py-2 bg-red-600 text-white rounded text-sm hover:bg-red-700">
                    Delete
                </button>
                <button type="button" @click="showDelete = false"
                        class="flex-1 px-4 py-2 bg-gray-700 text-white rounded text-sm hover:bg-gray-600">
                    Cancel
                </button>
            </form>
        </div>
    </div>
</div>

<script>
function torrentMgmt() {
    return {
        showAdd:       false,
        showDelete:    false,
        showFreeleech: false,
        dragover:      false,
        uploading:     false,
        torrentInfo:   null,
        uploadError:   null,
        addInfoHash:   '',
        search:        '',
        deleteHash:    '',
        deleteId:      null,
        freeleechHash: '',
        freeleechId:   null,

        filterRows(q) {
            const term  = q.trim().toLowerCase();
            const total = <?= count($torrents) ?>;
            let visible = 0;
            document.querySelectorAll('#torrentTableBody .torrent-row').forEach(row => {
                const show = !term || row.dataset.tid.includes(term)
                                   || row.dataset.hash.toLowerCase().includes(term);
                row.style.display = show ? '' : 'none';
                if (show) visible++;
            });
            const nr = document.getElementById('torrentNoResults');
            if (nr) nr.classList.toggle('hidden', visible > 0);
            const lbl = document.getElementById('torrentCount');
            if (lbl) lbl.innerHTML = term
                ? `${visible} / ${total} torrents <span class="text-gray-600">· peers: last 2 h</span>`
                : `${total} torrents <span class="text-gray-600">· peers: last 2 h</span>`;
        },

        confirmDelete(hash, id) {
            this.deleteHash = hash;
            this.deleteId   = id;
            this.showDelete = true;
        },

        openFreeleech(hash, id) {
            this.freeleechHash = hash;
            this.freeleechId   = id;
            this.showFreeleech = true;
        },

        handleDrop(e) {
            this.dragover = false;
            const files = e.dataTransfer.files;
            if (files.length > 0) this.uploadTorrent(files[0]);
        },

        handleFileSelect(e) {
            if (e.target.files.length > 0) this.uploadTorrent(e.target.files[0]);
        },

        async uploadTorrent(file) {
            if (!file.name.endsWith('.torrent')) {
                this.uploadError = 'Please select a .torrent file.';
                return;
            }
            this.uploading   = true;
            this.uploadError = null;
            const fd = new FormData();
            fd.append('torrent', file);
            try {
                const res  = await fetch('api/parse-torrent.php', { method: 'POST', body: fd });
                const data = await res.json();
                if (!res.ok || data.error) throw new Error(data.error || 'Parse failed');
                this.torrentInfo = data;
                this.addInfoHash = data.info_hash;
                setTimeout(() => lucide.createIcons(), 100);
            } catch (e) {
                this.uploadError = e.message;
            } finally {
                this.uploading = false;
            }
        },

        clearTorrent() {
            this.torrentInfo = null;
            this.addInfoHash = '';
            this.uploadError = null;
            this.$refs.fileInput.value = '';
        }
    };
}
</script>

<?php include 'includes/footer.php'; ?>
