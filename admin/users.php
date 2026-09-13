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
        if ($action === 'add') {
            $userID = (int)$_POST['user_id'];
            $passkey = $_POST['passkey'];
            $canLeech = isset($_POST['can_leech']);
            $isProtected = isset($_POST['is_protected']);

            if (strlen($passkey) !== 32) {
                throw new Exception('Passkey must be 32 characters');
            }

            TrackerAPI::updateUser($userID, $passkey, $canLeech, $isProtected);
            $success = "User $userID added successfully";

        } elseif ($action === 'delete') {
            $userID = (int)$_POST['user_id'];
            TrackerAPI::deleteUser($userID);
            $success = "User $userID removed successfully";
        }
    } catch (Exception $e) {
        $error = $e->getMessage();
    }
}

// Fetch users from database
try {
    $db = OcelotDB::connect();

    // Get unique users from peers table (active users)
    $activeUsers = $db->query("
        SELECT DISTINCT user_id,
               MAX(timestamp) as last_seen,
               SUM(uploaded) as uploaded,
               SUM(downloaded) as downloaded
        FROM peers
        GROUP BY user_id
        ORDER BY last_seen DESC
        LIMIT 100
    ")->fetchAll();

} catch (Exception $e) {
    $error = $e->getMessage();
    $activeUsers = [];
}

include 'includes/header.php';
?>

<div x-data="{ showAddModal: false, deleteUserId: null }">
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
                Showing <?= count($activeUsers) ?> active users
            </p>
        </div>
        <button @click="showAddModal = true"
                class="inline-flex items-center px-4 py-2 border border-transparent text-sm font-medium rounded-md text-white bg-blue-600 hover:bg-blue-700">
            <i data-lucide="user-plus" class="w-4 h-4 mr-2"></i>
            Add User
        </button>
    </div>

    <!-- Users Table -->
    <div class="bg-gray-800 shadow rounded-lg border border-gray-700 overflow-hidden">
        <table class="min-w-full divide-y divide-gray-700">
            <thead class="bg-gray-900">
                <tr>
                    <th class="px-6 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">User ID</th>
                    <th class="px-6 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">Uploaded</th>
                    <th class="px-6 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">Downloaded</th>
                    <th class="px-6 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">Ratio</th>
                    <th class="px-6 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">Last Seen</th>
                    <th class="px-6 py-3 text-right text-xs font-medium text-gray-400 uppercase tracking-wider">Actions</th>
                </tr>
            </thead>
            <tbody class="divide-y divide-gray-700">
                <?php if (empty($activeUsers)): ?>
                <tr>
                    <td colspan="6" class="px-6 py-8 text-center text-gray-400">
                        <i data-lucide="users" class="w-12 h-12 mx-auto mb-2 opacity-50"></i>
                        <p>No active users found</p>
                    </td>
                </tr>
                <?php else: ?>
                    <?php foreach ($activeUsers as $user): ?>
                    <?php
                    $ratio = $user['downloaded'] > 0 ? $user['uploaded'] / $user['downloaded'] : 0;
                    $ratioColor = $ratio >= 1 ? 'text-green-400' : 'text-red-400';
                    ?>
                    <tr>
                        <td class="px-6 py-4 whitespace-nowrap text-sm font-medium text-white">
                            <?= $user['user_id'] ?>
                        </td>
                        <td class="px-6 py-4 whitespace-nowrap text-sm text-green-400">
                            <?= formatBytes($user['uploaded']) ?>
                        </td>
                        <td class="px-6 py-4 whitespace-nowrap text-sm text-blue-400">
                            <?= formatBytes($user['downloaded']) ?>
                        </td>
                        <td class="px-6 py-4 whitespace-nowrap text-sm <?= $ratioColor ?>">
                            <?= number_format($ratio, 2) ?>
                        </td>
                        <td class="px-6 py-4 whitespace-nowrap text-sm text-gray-400">
                            <?= timeAgo($user['last_seen']) ?>
                        </td>
                        <td class="px-6 py-4 whitespace-nowrap text-right text-sm font-medium">
                            <button @click="deleteUserId = <?= $user['user_id'] ?>"
                                    class="text-red-400 hover:text-red-300">
                                <i data-lucide="trash-2" class="w-4 h-4"></i>
                            </button>
                        </td>
                    </tr>
                    <?php endforeach; ?>
                <?php endif; ?>
            </tbody>
        </table>
    </div>

    <!-- Add User Modal -->
    <div x-show="showAddModal" x-cloak
         class="fixed inset-0 bg-gray-900 bg-opacity-75 overflow-y-auto h-full w-full z-50"
         @click.self="showAddModal = false">
        <div class="relative top-20 mx-auto p-5 border w-96 shadow-lg rounded-md bg-gray-800 border-gray-700">
            <div class="flex justify-between items-center mb-4">
                <h3 class="text-lg font-medium text-white">Add New User</h3>
                <button @click="showAddModal = false" class="text-gray-400 hover:text-white">
                    <i data-lucide="x" class="w-5 h-5"></i>
                </button>
            </div>

            <form method="POST" class="space-y-4">
                <input type="hidden" name="action" value="add">

                <div>
                    <label class="block text-sm font-medium text-gray-300 mb-1">User ID</label>
                    <input type="number" name="user_id" required
                           class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500">
                </div>

                <div>
                    <label class="block text-sm font-medium text-gray-300 mb-1">Passkey (32 chars)</label>
                    <div class="flex gap-2">
                        <input type="text" name="passkey" id="passkey" maxlength="32" required
                               class="flex-1 px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500">
                        <button type="button" onclick="generatePasskey()"
                                class="px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white hover:bg-gray-600">
                            <i data-lucide="refresh-cw" class="w-4 h-4"></i>
                        </button>
                    </div>
                    <p class="text-xs text-gray-400 mt-1" id="passkeyLength">0/32</p>
                </div>

                <div class="flex items-center">
                    <input type="checkbox" name="can_leech" id="can_leech" checked
                           class="w-4 h-4 text-blue-600 bg-gray-700 border-gray-600 rounded focus:ring-blue-500">
                    <label for="can_leech" class="ml-2 text-sm text-gray-300">Can Leech</label>
                </div>

                <div class="flex items-center">
                    <input type="checkbox" name="is_protected" id="is_protected"
                           class="w-4 h-4 text-blue-600 bg-gray-700 border-gray-600 rounded focus:ring-blue-500">
                    <label for="is_protected" class="ml-2 text-sm text-gray-300">IP Protection</label>
                </div>

                <div class="flex gap-2 pt-4">
                    <button type="submit"
                            class="flex-1 px-4 py-2 bg-blue-600 text-white rounded-md hover:bg-blue-700">
                        Add User
                    </button>
                    <button type="button" @click="showAddModal = false"
                            class="flex-1 px-4 py-2 bg-gray-700 text-white rounded-md hover:bg-gray-600">
                        Cancel
                    </button>
                </div>
            </form>
        </div>
    </div>

    <!-- Delete Confirmation Modal -->
    <div x-show="deleteUserId !== null" x-cloak
         class="fixed inset-0 bg-gray-900 bg-opacity-75 overflow-y-auto h-full w-full z-50"
         @click.self="deleteUserId = null">
        <div class="relative top-20 mx-auto p-5 border w-96 shadow-lg rounded-md bg-gray-800 border-gray-700">
            <h3 class="text-lg font-medium text-white mb-4">Confirm Delete</h3>
            <p class="text-gray-300 mb-6">Are you sure you want to remove this user? This action cannot be undone.</p>

            <form method="POST" class="flex gap-2">
                <input type="hidden" name="action" value="delete">
                <input type="hidden" name="user_id" :value="deleteUserId">

                <button type="submit"
                        class="flex-1 px-4 py-2 bg-red-600 text-white rounded-md hover:bg-red-700">
                    Delete
                </button>
                <button type="button" @click="deleteUserId = null"
                        class="flex-1 px-4 py-2 bg-gray-700 text-white rounded-md hover:bg-gray-600">
                    Cancel
                </button>
            </form>
        </div>
    </div>
</div>

<script>
function generatePasskey() {
    const chars = '0123456789abcdef';
    let passkey = '';
    for (let i = 0; i < 32; i++) {
        passkey += chars[Math.floor(Math.random() * chars.length)];
    }
    document.getElementById('passkey').value = passkey;
    updatePasskeyLength();
}

document.getElementById('passkey')?.addEventListener('input', updatePasskeyLength);

function updatePasskeyLength() {
    const input = document.getElementById('passkey');
    const length = input.value.length;
    document.getElementById('passkeyLength').textContent = `${length}/32`;
}
</script>

<?php include 'includes/footer.php'; ?>
