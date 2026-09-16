<?php
require_once 'config.php';
requireAuth();

$pageTitle = 'User Management';
$success = '';
$error = '';

// Handle user actions
if ($_SERVER['REQUEST_METHOD'] === 'POST') {
    $action = $_POST['action'] ?? '';

    try {
        $db = OcelotDB::connect();

        if ($action === 'add') {
            $userID  = (int)$_POST['user_id'];
            $passkey = trim($_POST['passkey'] ?? '');
            $canLeech    = isset($_POST['can_leech']);
            $isProtected = isset($_POST['is_protected']);

            if ($userID <= 0) {
                throw new Exception('User ID must be a positive integer.');
            }
            if (strlen($passkey) !== 32 || !ctype_xdigit($passkey)) {
                throw new Exception('Passkey must be 32 hex characters.');
            }

            TrackerAPI::addUser($userID, $passkey, $canLeech, $isProtected);
            $success = "User $userID added to tracker.";

        } elseif ($action === 'update') {
            $userID      = (int)$_POST['user_id'];
            $canLeech    = isset($_POST['can_leech']);
            $isProtected = isset($_POST['is_protected']);

            $row = $db->prepare("SELECT passkey FROM user_passkeys WHERE user_id = ?");
            $row->execute([$userID]);
            $user = $row->fetch();
            if (!$user) {
                throw new Exception("User $userID not found in user_passkeys.");
            }

            TrackerAPI::updateUser($user['passkey'], $canLeech, $isProtected);
            $success = "User $userID updated.";

        } elseif ($action === 'delete') {
            $userID = (int)$_POST['user_id'];

            $stmt = $db->prepare("SELECT passkey FROM user_passkeys WHERE user_id = ?");
            $stmt->execute([$userID]);
            $user = $stmt->fetch();
            if (!$user) {
                throw new Exception("User $userID not found in user_passkeys.");
            }

            TrackerAPI::deleteUser($user['passkey']);
            $success = "User $userID removed from tracker.";

        } elseif ($action === 'change_passkey') {
            $userID     = (int)$_POST['user_id'];
            $newPasskey = trim($_POST['new_passkey'] ?? '');

            if (strlen($newPasskey) !== 32 || !ctype_xdigit($newPasskey)) {
                throw new Exception('New passkey must be 32 hex characters.');
            }

            $stmt = $db->prepare("SELECT passkey FROM user_passkeys WHERE user_id = ?");
            $stmt->execute([$userID]);
            $user = $stmt->fetch();
            if (!$user) {
                throw new Exception("User $userID not found in user_passkeys.");
            }

            TrackerAPI::changePasskey($user['passkey'], $newPasskey);
            $success = "Passkey rotated for user $userID.";

        } else {
            throw new Exception('Unknown action.');
        }

    } catch (Exception $e) {
        $error = $e->getMessage();
    }
}

// Fetch users from user_passkeys (registry) + join for stats
$users = [];
try {
    $db = OcelotDB::connect();

    $users = $db->query("
        SELECT up.user_id,
               up.passkey,
               up.can_leech,
               up.protect_ip,
               COALESCE(u.uploaded,   0) as uploaded,
               COALESCE(u.downloaded, 0) as downloaded,
               COALESCE(ps.snatch_count, 0) as snatch_count,
               MAX(p.timestamp) as last_seen
        FROM user_passkeys up
        LEFT JOIN users u        ON u.id        = up.user_id
        LEFT JOIN (
            SELECT user_id, MAX(timestamp) as timestamp
            FROM peers GROUP BY user_id
        ) p ON p.user_id = up.user_id
        LEFT JOIN (
            SELECT user_id, COUNT(*) as snatch_count
            FROM snatches GROUP BY user_id
        ) ps ON ps.user_id = up.user_id
        GROUP BY up.user_id
        ORDER BY last_seen DESC NULLS LAST
        LIMIT 500
    ")->fetchAll();

} catch (Exception $e) {
    $error = $error ?: $e->getMessage();
}

include 'includes/header.php';
?>

<div x-data="userMgmt()">
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
            <input type="text" x-model="search" placeholder="Filter by user ID or passkey…"
                   class="w-full pl-9 pr-3 py-2 bg-gray-700 border border-gray-600 rounded text-white text-sm focus:outline-none focus:ring-2 focus:ring-blue-500">
        </div>
        <p class="text-sm text-gray-400 whitespace-nowrap" id="userCount">
            <?= number_format(count($users)) ?> users
        </p>
        <button @click="showAdd = true"
                class="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-md text-sm hover:bg-blue-700">
            <i data-lucide="user-plus" class="w-4 h-4"></i>Add User
        </button>
    </div>

    <!-- Users Table -->
    <div class="bg-gray-800 border border-gray-700 rounded-lg overflow-hidden">
        <div class="overflow-x-auto">
            <table class="min-w-full divide-y divide-gray-700 text-sm">
                <thead class="bg-gray-900">
                    <tr>
                        <th class="px-5 py-3 text-left text-xs text-gray-400 uppercase">ID</th>
                        <th class="px-5 py-3 text-left text-xs text-gray-400 uppercase">Passkey</th>
                        <th class="px-5 py-3 text-left text-xs text-gray-400 uppercase">Flags</th>
                        <th class="px-5 py-3 text-left text-xs text-gray-400 uppercase">Uploaded</th>
                        <th class="px-5 py-3 text-left text-xs text-gray-400 uppercase">Downloaded</th>
                        <th class="px-5 py-3 text-left text-xs text-gray-400 uppercase">Ratio</th>
                        <th class="px-5 py-3 text-left text-xs text-gray-400 uppercase">Snatches</th>
                        <th class="px-5 py-3 text-left text-xs text-gray-400 uppercase">Last Seen</th>
                        <th class="px-5 py-3 text-right text-xs text-gray-400 uppercase">Actions</th>
                    </tr>
                </thead>
                <tbody class="divide-y divide-gray-700" id="userTableBody">
                    <?php if (empty($users)): ?>
                    <tr>
                        <td colspan="9" class="px-5 py-8 text-center text-gray-500">
                            <i data-lucide="users" class="w-8 h-8 mx-auto mb-2 opacity-40"></i>
                            <p>No users registered</p>
                        </td>
                    </tr>
                    <?php else: foreach ($users as $u):
                        $ratio = $u['downloaded'] > 0 ? $u['uploaded'] / $u['downloaded'] : ($u['uploaded'] > 0 ? INF : 0);
                        $ratioLabel = is_infinite($ratio) ? '∞' : number_format($ratio, 2);
                        $ratioColor = $ratio >= 1.0 ? 'text-green-400' : ($ratio >= 0.5 ? 'text-yellow-400' : 'text-red-400');
                    ?>
                    <tr class="hover:bg-gray-750 user-row"
                        data-uid="<?= $u['user_id'] ?>"
                        data-passkey="<?= htmlspecialchars($u['passkey']) ?>">
                        <td class="px-5 py-3 font-mono text-white"><?= $u['user_id'] ?></td>
                        <td class="px-5 py-3 font-mono text-xs text-gray-400 group relative">
                            <span class="blur-sm group-hover:blur-none transition-all duration-200 select-all"
                                  title="hover to reveal"><?= htmlspecialchars($u['passkey']) ?></span>
                        </td>
                        <td class="px-5 py-3">
                            <div class="flex gap-1">
                                <?php if ($u['can_leech']): ?>
                                <span class="px-1.5 py-0.5 rounded text-xs bg-green-900 text-green-300">leech</span>
                                <?php else: ?>
                                <span class="px-1.5 py-0.5 rounded text-xs bg-red-900 text-red-300">no-leech</span>
                                <?php endif; ?>
                                <?php if ($u['protect_ip']): ?>
                                <span class="px-1.5 py-0.5 rounded text-xs bg-blue-900 text-blue-300">ip-prot</span>
                                <?php endif; ?>
                            </div>
                        </td>
                        <td class="px-5 py-3 text-green-400"><?= formatBytes($u['uploaded']) ?></td>
                        <td class="px-5 py-3 text-blue-400"><?= formatBytes($u['downloaded']) ?></td>
                        <td class="px-5 py-3 <?= $ratioColor ?>"><?= $ratioLabel ?></td>
                        <td class="px-5 py-3 text-purple-400"><?= number_format($u['snatch_count']) ?></td>
                        <td class="px-5 py-3 text-gray-400 text-xs">
                            <?= $u['last_seen'] ? timeAgo($u['last_seen']) : '<span class="text-gray-600">never</span>' ?>
                        </td>
                        <td class="px-5 py-3 text-right">
                            <div class="flex items-center justify-end gap-3">
                                <button @click="openEdit(<?= $u['user_id'] ?>, <?= $u['can_leech'] ? 'true' : 'false' ?>, <?= $u['protect_ip'] ? 'true' : 'false' ?>)"
                                        class="text-gray-400 hover:text-white" title="Edit flags">
                                    <i data-lucide="pencil" class="w-4 h-4"></i>
                                </button>
                                <button @click="openRotate(<?= $u['user_id'] ?>)"
                                        class="text-yellow-400 hover:text-yellow-300" title="Rotate passkey">
                                    <i data-lucide="refresh-cw" class="w-4 h-4"></i>
                                </button>
                                <button @click="confirmDelete(<?= $u['user_id'] ?>)"
                                        class="text-red-400 hover:text-red-300" title="Delete">
                                    <i data-lucide="trash-2" class="w-4 h-4"></i>
                                </button>
                            </div>
                        </td>
                    </tr>
                    <?php endforeach; endif; ?>
                    <tr id="noResultsRow" class="hidden">
                        <td colspan="9" class="px-5 py-6 text-center text-gray-500">No users match the filter.</td>
                    </tr>
                </tbody>
            </table>
        </div>
    </div>

    <!-- Add User Modal -->
    <div x-show="showAdd" x-cloak
         class="fixed inset-0 bg-black/70 flex items-center justify-center z-50"
         @click.self="showAdd = false">
        <div class="bg-gray-800 border border-gray-700 rounded-lg p-6 w-96 shadow-xl">
            <div class="flex justify-between items-center mb-5">
                <h3 class="text-lg font-semibold text-white">Add User</h3>
                <button @click="showAdd = false" class="text-gray-400 hover:text-white">
                    <i data-lucide="x" class="w-5 h-5"></i>
                </button>
            </div>
            <form method="POST" class="space-y-4">
                <input type="hidden" name="action" value="add">
                <div>
                    <label class="block text-xs text-gray-400 mb-1">User ID</label>
                    <input type="number" name="user_id" required min="1"
                           class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-white text-sm focus:outline-none focus:ring-2 focus:ring-blue-500">
                </div>
                <div>
                    <label class="block text-xs text-gray-400 mb-1">Passkey <span class="text-gray-600">(32 hex chars)</span></label>
                    <div class="flex gap-2">
                        <input type="text" name="passkey" id="addPasskey" maxlength="32"
                               x-model="newPasskey" required
                               class="flex-1 px-3 py-2 bg-gray-700 border border-gray-600 rounded text-white text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500">
                        <button type="button" @click="newPasskey = genPasskey()"
                                class="px-3 py-2 bg-gray-700 border border-gray-600 rounded text-white hover:bg-gray-600" title="Generate">
                            <i data-lucide="shuffle" class="w-4 h-4"></i>
                        </button>
                    </div>
                    <p class="text-xs mt-1" :class="newPasskey.length === 32 ? 'text-green-400' : 'text-gray-500'">
                        <span x-text="newPasskey.length"></span>/32
                    </p>
                </div>
                <div class="flex gap-4">
                    <label class="flex items-center gap-2 text-sm text-gray-300 cursor-pointer">
                        <input type="checkbox" name="can_leech" checked
                               class="w-4 h-4 bg-gray-700 border-gray-600 rounded text-blue-600">
                        Can Leech
                    </label>
                    <label class="flex items-center gap-2 text-sm text-gray-300 cursor-pointer">
                        <input type="checkbox" name="is_protected"
                               class="w-4 h-4 bg-gray-700 border-gray-600 rounded text-blue-600">
                        IP Protection
                    </label>
                </div>
                <div class="flex gap-2 pt-2">
                    <button type="submit"
                            class="flex-1 px-4 py-2 bg-blue-600 text-white rounded text-sm hover:bg-blue-700">
                        Add User
                    </button>
                    <button type="button" @click="showAdd = false"
                            class="flex-1 px-4 py-2 bg-gray-700 text-white rounded text-sm hover:bg-gray-600">
                        Cancel
                    </button>
                </div>
            </form>
        </div>
    </div>

    <!-- Edit Flags Modal -->
    <div x-show="showEdit" x-cloak
         class="fixed inset-0 bg-black/70 flex items-center justify-center z-50"
         @click.self="showEdit = false">
        <div class="bg-gray-800 border border-gray-700 rounded-lg p-6 w-80 shadow-xl">
            <h3 class="text-lg font-semibold text-white mb-5">Edit User <span x-text="editId" class="text-blue-400"></span></h3>
            <form method="POST" class="space-y-4">
                <input type="hidden" name="action" value="update">
                <input type="hidden" name="user_id" :value="editId">
                <div class="flex gap-4">
                    <label class="flex items-center gap-2 text-sm text-gray-300 cursor-pointer">
                        <input type="checkbox" name="can_leech" :checked="editCanLeech"
                               @change="editCanLeech = $event.target.checked"
                               class="w-4 h-4 bg-gray-700 border-gray-600 rounded text-blue-600">
                        Can Leech
                    </label>
                    <label class="flex items-center gap-2 text-sm text-gray-300 cursor-pointer">
                        <input type="checkbox" name="is_protected" :checked="editProtectIp"
                               @change="editProtectIp = $event.target.checked"
                               class="w-4 h-4 bg-gray-700 border-gray-600 rounded text-blue-600">
                        IP Protection
                    </label>
                </div>
                <div class="flex gap-2 pt-2">
                    <button type="submit"
                            class="flex-1 px-4 py-2 bg-blue-600 text-white rounded text-sm hover:bg-blue-700">
                        Save
                    </button>
                    <button type="button" @click="showEdit = false"
                            class="flex-1 px-4 py-2 bg-gray-700 text-white rounded text-sm hover:bg-gray-600">
                        Cancel
                    </button>
                </div>
            </form>
        </div>
    </div>

    <!-- Rotate Passkey Modal -->
    <div x-show="showRotate" x-cloak
         class="fixed inset-0 bg-black/70 flex items-center justify-center z-50"
         @click.self="showRotate = false">
        <div class="bg-gray-800 border border-gray-700 rounded-lg p-6 w-96 shadow-xl">
            <h3 class="text-lg font-semibold text-white mb-1">Rotate Passkey</h3>
            <p class="text-sm text-gray-400 mb-5">User <span x-text="rotateId" class="text-blue-400 font-mono"></span></p>
            <form method="POST" class="space-y-4">
                <input type="hidden" name="action" value="change_passkey">
                <input type="hidden" name="user_id" :value="rotateId">
                <div>
                    <label class="block text-xs text-gray-400 mb-1">New Passkey</label>
                    <div class="flex gap-2">
                        <input type="text" name="new_passkey" maxlength="32"
                               x-model="rotatePasskey" required
                               class="flex-1 px-3 py-2 bg-gray-700 border border-gray-600 rounded text-white text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500">
                        <button type="button" @click="rotatePasskey = genPasskey()"
                                class="px-3 py-2 bg-gray-700 border border-gray-600 rounded text-white hover:bg-gray-600">
                            <i data-lucide="shuffle" class="w-4 h-4"></i>
                        </button>
                    </div>
                </div>
                <div class="flex gap-2 pt-2">
                    <button type="submit"
                            class="flex-1 px-4 py-2 bg-yellow-600 text-white rounded text-sm hover:bg-yellow-700">
                        Rotate
                    </button>
                    <button type="button" @click="showRotate = false"
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
            <h3 class="text-lg font-semibold text-white mb-2">Delete User</h3>
            <p class="text-sm text-gray-300 mb-6">
                Remove user <span x-text="deleteId" class="text-red-400 font-mono font-bold"></span> from the tracker?
                This cannot be undone.
            </p>
            <form method="POST" class="flex gap-2">
                <input type="hidden" name="action" value="delete">
                <input type="hidden" name="user_id" :value="deleteId">
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
function userMgmt() {
    return {
        showAdd:    false,
        showEdit:   false,
        showDelete: false,
        showRotate: false,
        search:        '',
        filtered:      [],
        newPasskey:    '',
        rotatePasskey: '',
        editId:        null,
        editCanLeech:  true,
        editProtectIp: false,
        deleteId:      null,
        rotateId:      null,

        init() {
            this.filtered = Array.from(document.querySelectorAll('#userTableBody .user-row'));
            this.$watch('search', v => this.filterRows(v));
        },

        filterRows(q) {
            const term  = q.trim().toLowerCase();
            const total = <?= count($users) ?>;
            let visible = 0;
            document.querySelectorAll('#userTableBody .user-row').forEach(row => {
                const show = !term || row.dataset.uid.includes(term)
                                   || row.dataset.passkey.toLowerCase().includes(term);
                row.style.display = show ? '' : 'none';
                if (show) visible++;
            });
            const noResults = document.getElementById('noResultsRow');
            if (noResults) noResults.classList.toggle('hidden', visible > 0);
            const lbl = document.getElementById('userCount');
            if (lbl) lbl.textContent = term
                ? `${visible} / ${total} users`
                : `${total} users`;
        },

        genPasskey() {
            const buf = new Uint8Array(16);
            crypto.getRandomValues(buf);
            return Array.from(buf).map(b => b.toString(16).padStart(2, '0')).join('');
        },

        openEdit(id, canLeech, protectIp) {
            this.editId = id;
            this.editCanLeech  = canLeech;
            this.editProtectIp = protectIp;
            this.showEdit = true;
        },

        openRotate(id) {
            this.rotateId = id;
            this.rotatePasskey = this.genPasskey();
            this.showRotate = true;
        },

        confirmDelete(id) {
            this.deleteId = id;
            this.showDelete = true;
        }
    };
}
</script>

<?php include 'includes/footer.php'; ?>
