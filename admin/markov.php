<?php
require_once 'config.php';
requireAuth();

$pageTitle = 'Markov Analytics';

// --- Fetch all data from sidecar (graceful null on offline) ---
$health   = MarkovAPI::health();
$metrics  = MarkovAPI::metrics();
$chainP   = MarkovAPI::chainPeer();
$chainU   = MarkovAPI::chainUser();
$chainT   = MarkovAPI::chainTorrent();
$freeleech = MarkovAPI::freeleech();
$pageRank  = MarkovAPI::seederPageRank();

$sidecarOnline = ($health !== null && ($health['status'] ?? '') === 'ok');

// State label arrays (must match chain/states.go)
$peerStates    = ['LEECHING','SEEDING','DORMANT','SNATCHED','DEAD'];
$userStates    = ['HEALTHY','WARNING','PROBATION','BANNED','FREELEECH'];
$torrentStates = ['THRIVING','HEALTHY','AT_RISK','DYING','DEAD'];

// --- Build chain matrix arrays for JSON injection ---
function chainToMatrix(array $rows, array $stateNames): array {
    $n = count($stateNames);
    $matrix = [];
    foreach ($rows as $row) {
        $from = array_search($row['from'], $stateNames, true);
        if ($from === false) continue;
        foreach ($row['transitions'] as $toName => $prob) {
            $to = array_search($toName, $stateNames, true);
            if ($to === false) continue;
            $matrix[] = ['from' => (int)$from, 'to' => (int)$to, 'prob' => (float)$prob];
        }
    }
    return $matrix;
}

$matrixPeer    = $chainP ? chainToMatrix($chainP, $peerStates)    : [];
$matrixUser    = $chainU ? chainToMatrix($chainU, $userStates)    : [];
$matrixTorrent = $chainT ? chainToMatrix($chainT, $torrentStates) : [];

// State badge colors
$stateBadgeClass = [
    'HEALTHY'   => 'bg-green-700 text-green-200',
    'THRIVING'  => 'bg-green-700 text-green-200',
    'SEEDING'   => 'bg-green-700 text-green-200',
    'WARNING'   => 'bg-yellow-700 text-yellow-200',
    'AT_RISK'   => 'bg-yellow-700 text-yellow-200',
    'PROBATION' => 'bg-orange-700 text-orange-200',
    'DYING'     => 'bg-orange-700 text-orange-200',
    'LEECHING'  => 'bg-blue-700 text-blue-200',
    'DORMANT'   => 'bg-gray-600 text-gray-200',
    'SNATCHED'  => 'bg-purple-700 text-purple-200',
    'FREELEECH' => 'bg-indigo-700 text-indigo-200',
    'BANNED'    => 'bg-red-700 text-red-200',
    'DEAD'      => 'bg-red-900 text-red-300',
];

include 'includes/header.php';
?>

<!-- Service Status Banner -->
<div class="mb-6 flex items-center gap-3 p-4 rounded-lg border <?= $sidecarOnline ? 'bg-green-900 border-green-700' : 'bg-red-900 border-red-700' ?>">
    <i data-lucide="<?= $sidecarOnline ? 'check-circle' : 'alert-triangle' ?>"
       class="w-5 h-5 <?= $sidecarOnline ? 'text-green-400' : 'text-red-400' ?>"></i>
    <span class="text-sm font-medium <?= $sidecarOnline ? 'text-green-200' : 'text-red-200' ?>">
        <?= $sidecarOnline ? 'Markov sidecar online — ' . MARKOV_URL : 'Markov sidecar offline or unreachable at ' . MARKOV_URL ?>
    </span>
    <?php if ($sidecarOnline && $metrics): ?>
    <span class="ml-auto text-xs text-gray-400">
        poll #<?= number_format($metrics['poll_count']) ?> &bull;
        watermark <?= date('Y-m-d H:i', $metrics['snatch_watermark'] ?? 0) ?>
    </span>
    <?php endif; ?>
</div>

<!-- Metrics Row -->
<?php if ($metrics): ?>
<div class="grid grid-cols-2 sm:grid-cols-4 gap-4 mb-8">
    <?php
    $metricCards = [
        ['label' => 'Tracked Peers',    'value' => number_format($metrics['tracked_peers']),    'icon' => 'radio',         'color' => 'text-blue-400'],
        ['label' => 'Tracked Torrents', 'value' => number_format($metrics['tracked_torrents']), 'icon' => 'hard-drive',    'color' => 'text-purple-400'],
        ['label' => 'Tracked Users',    'value' => number_format($metrics['tracked_users']),    'icon' => 'users',         'color' => 'text-green-400'],
        ['label' => 'Poll Count',       'value' => number_format($metrics['poll_count']),        'icon' => 'activity',      'color' => 'text-yellow-400'],
    ];
    foreach ($metricCards as $card):
    ?>
    <div class="bg-gray-800 border border-gray-700 rounded-lg p-4 flex items-center gap-4">
        <i data-lucide="<?= $card['icon'] ?>" class="w-8 h-8 <?= $card['color'] ?> flex-shrink-0"></i>
        <div>
            <div class="text-2xl font-bold text-white"><?= $card['value'] ?></div>
            <div class="text-xs text-gray-400"><?= $card['label'] ?></div>
        </div>
    </div>
    <?php endforeach; ?>
</div>
<?php endif; ?>

<!-- Transition Matrix Heatmaps -->
<div class="mb-8">
    <h2 class="text-xl font-semibold text-white mb-4 flex items-center gap-2">
        <i data-lucide="grid" class="w-5 h-5 text-purple-400"></i>
        Markov Transition Matrices
    </h2>
    <div class="grid grid-cols-1 xl:grid-cols-3 gap-6">
        <div class="bg-gray-800 border border-gray-700 rounded-lg p-5">
            <h3 class="text-sm font-semibold text-gray-300 uppercase tracking-wider mb-3">Peer Lifecycle</h3>
            <div id="heatmap-peer" class="w-full"></div>
        </div>
        <div class="bg-gray-800 border border-gray-700 rounded-lg p-5">
            <h3 class="text-sm font-semibold text-gray-300 uppercase tracking-wider mb-3">User Ratio Health</h3>
            <div id="heatmap-user" class="w-full"></div>
        </div>
        <div class="bg-gray-800 border border-gray-700 rounded-lg p-5">
            <h3 class="text-sm font-semibold text-gray-300 uppercase tracking-wider mb-3">Torrent Swarm Health</h3>
            <div id="heatmap-torrent" class="w-full"></div>
        </div>
    </div>
    <?php if (!$sidecarOnline): ?>
    <p class="text-center text-gray-500 text-sm mt-4">Heatmaps unavailable — sidecar offline</p>
    <?php endif; ?>
</div>

<!-- PageRank + Freeleech Row -->
<div class="grid grid-cols-1 lg:grid-cols-2 gap-6 mb-8">

    <!-- Seeder PageRank -->
    <div class="bg-gray-800 border border-gray-700 rounded-lg overflow-hidden">
        <div class="px-5 py-4 border-b border-gray-700 flex items-center gap-2">
            <i data-lucide="award" class="w-5 h-5 text-yellow-400"></i>
            <h2 class="text-lg font-semibold text-white">Seeder PageRank</h2>
            <span class="ml-auto text-xs text-gray-500">stationary π × path health</span>
        </div>
        <div class="overflow-x-auto">
            <table class="min-w-full divide-y divide-gray-700 text-sm">
                <thead class="bg-gray-900">
                    <tr>
                        <th class="px-4 py-3 text-left text-xs text-gray-400 uppercase">#</th>
                        <th class="px-4 py-3 text-left text-xs text-gray-400 uppercase">UID</th>
                        <th class="px-4 py-3 text-left text-xs text-gray-400 uppercase">Score</th>
                        <th class="px-4 py-3 text-left text-xs text-gray-400 uppercase">State</th>
                    </tr>
                </thead>
                <tbody class="divide-y divide-gray-700">
                    <?php if (empty($pageRank)): ?>
                    <tr>
                        <td colspan="4" class="px-4 py-6 text-center text-gray-500">
                            <?= $sidecarOnline ? 'No users tracked yet' : 'Sidecar offline' ?>
                        </td>
                    </tr>
                    <?php else:
                        foreach (array_slice($pageRank, 0, 25) as $i => $r):
                            $stateLabel = $r['state'] ?? '—';
                            $badgeClass = $stateBadgeClass[$stateLabel] ?? 'bg-gray-700 text-gray-300';
                    ?>
                    <tr class="hover:bg-gray-750">
                        <td class="px-4 py-3 text-gray-500 font-mono"><?= $i + 1 ?></td>
                        <td class="px-4 py-3 text-white font-medium"><?= (int)$r['uid'] ?></td>
                        <td class="px-4 py-3 font-mono text-blue-300"><?= number_format((float)$r['score'], 6) ?></td>
                        <td class="px-4 py-3">
                            <span class="px-2 py-0.5 rounded text-xs font-medium <?= $badgeClass ?>">
                                <?= htmlspecialchars($stateLabel) ?>
                            </span>
                        </td>
                    </tr>
                    <?php endforeach; endif; ?>
                </tbody>
            </table>
        </div>
    </div>

    <!-- Freeleech Candidates -->
    <div class="bg-gray-800 border border-gray-700 rounded-lg overflow-hidden">
        <div class="px-5 py-4 border-b border-gray-700 flex items-center gap-2">
            <i data-lucide="zap" class="w-5 h-5 text-green-400"></i>
            <h2 class="text-lg font-semibold text-white">Freeleech Candidates</h2>
            <span class="ml-auto text-xs text-gray-500">ranked by priority score</span>
        </div>
        <div class="overflow-x-auto">
            <table class="min-w-full divide-y divide-gray-700 text-sm">
                <thead class="bg-gray-900">
                    <tr>
                        <th class="px-4 py-3 text-left text-xs text-gray-400 uppercase">#</th>
                        <th class="px-4 py-3 text-left text-xs text-gray-400 uppercase">Torrent ID</th>
                        <th class="px-4 py-3 text-left text-xs text-gray-400 uppercase">Priority</th>
                        <th class="px-4 py-3 text-left text-xs text-gray-400 uppercase">Dead 72h</th>
                        <th class="px-4 py-3 text-left text-xs text-gray-400 uppercase">Risk</th>
                    </tr>
                </thead>
                <tbody class="divide-y divide-gray-700">
                    <?php if (empty($freeleech)): ?>
                    <tr>
                        <td colspan="5" class="px-4 py-6 text-center text-gray-500">
                            <?= $sidecarOnline ? 'No candidates yet' : 'Sidecar offline' ?>
                        </td>
                    </tr>
                    <?php else:
                        foreach (array_slice($freeleech, 0, 20) as $i => $c):
                            $dead72 = (float)($c['dead_prob_72h'] ?? 0);
                            if ($dead72 >= 0.7)      { $riskClass = 'bg-red-700 text-red-200';    $riskLabel = 'HIGH'; }
                            elseif ($dead72 >= 0.4)  { $riskClass = 'bg-yellow-700 text-yellow-200'; $riskLabel = 'MED'; }
                            else                     { $riskClass = 'bg-green-700 text-green-200'; $riskLabel = 'LOW'; }
                    ?>
                    <tr class="hover:bg-gray-750">
                        <td class="px-4 py-3 text-gray-500 font-mono"><?= $i + 1 ?></td>
                        <td class="px-4 py-3 text-white font-medium"><?= (int)$c['torrent_id'] ?></td>
                        <td class="px-4 py-3 font-mono text-purple-300"><?= number_format((float)$c['priority_score'], 4) ?></td>
                        <td class="px-4 py-3 font-mono <?= $dead72 >= 0.5 ? 'text-red-400' : 'text-gray-300' ?>">
                            <?= number_format($dead72 * 100, 1) ?>%
                        </td>
                        <td class="px-4 py-3">
                            <span class="px-2 py-0.5 rounded text-xs font-medium <?= $riskClass ?>">
                                <?= $riskLabel ?>
                            </span>
                        </td>
                    </tr>
                    <?php endforeach; endif; ?>
                </tbody>
            </table>
        </div>
    </div>
</div>

<!-- Torrent Health Predictor (live lookup) -->
<div class="bg-gray-800 border border-gray-700 rounded-lg p-6 mb-6" x-data="torrentLookup()">
    <h2 class="text-lg font-semibold text-white mb-4 flex items-center gap-2">
        <i data-lucide="search" class="w-5 h-5 text-blue-400"></i>
        Torrent Health Predictor
    </h2>
    <div class="flex gap-3 mb-4">
        <input type="number" x-model="torrentId" placeholder="Torrent ID"
               @keyup.enter="lookup()"
               class="w-40 px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white text-sm focus:outline-none focus:ring-2 focus:ring-blue-500">
        <button @click="lookup()"
                :disabled="loading"
                class="px-4 py-2 bg-blue-600 text-white rounded-md text-sm hover:bg-blue-700 disabled:opacity-50">
            <span x-show="!loading">Predict</span>
            <span x-show="loading">Loading…</span>
        </button>
    </div>

    <div x-show="result" x-cloak class="grid grid-cols-2 sm:grid-cols-4 gap-4">
        <div class="bg-gray-900 rounded p-3 text-center">
            <div class="text-xs text-gray-400 mb-1">Health State</div>
            <div class="text-sm font-bold" :class="stateColor(result?.health_state)">
                <span x-text="result?.health_state"></span>
            </div>
        </div>
        <div class="bg-gray-900 rounded p-3 text-center">
            <div class="text-xs text-gray-400 mb-1">Dead Prob 24h</div>
            <div class="text-lg font-mono font-bold" :class="result?.dead_prob_24h > 0.5 ? 'text-red-400' : 'text-green-400'">
                <span x-text="((result?.dead_prob_24h ?? 0)*100).toFixed(1) + '%'"></span>
            </div>
        </div>
        <div class="bg-gray-900 rounded p-3 text-center">
            <div class="text-xs text-gray-400 mb-1">Dead Prob 72h</div>
            <div class="text-lg font-mono font-bold" :class="result?.dead_prob_72h > 0.5 ? 'text-red-400' : 'text-green-400'">
                <span x-text="((result?.dead_prob_72h ?? 0)*100).toFixed(1) + '%'"></span>
            </div>
        </div>
        <div class="bg-gray-900 rounded p-3 text-center">
            <div class="text-xs text-gray-400 mb-1">Expected Dead (hrs)</div>
            <div class="text-lg font-mono font-bold text-yellow-400">
                <span x-text="(result?.expected_dead_hours ?? 0).toFixed(1)"></span>
            </div>
        </div>
    </div>

    <div x-show="error" x-cloak class="text-red-400 text-sm mt-2" x-text="error"></div>
</div>

<script>
// ── Heatmap renderer ─────────────────────────────────────────────────────────
function renderHeatmap(containerId, cells, labels) {
    const container = document.getElementById(containerId);
    if (!container) return;

    const n = labels.length;
    const size = Math.min(container.clientWidth, 340);
    const cellSize = (size - 48) / n;
    const marginLeft = 68, marginTop = 48;
    const W = cellSize * n + marginLeft;
    const H = cellSize * n + marginTop + 16;

    const colorScale = d3.scaleSequential()
        .domain([0, 1])
        .interpolator(d3.interpolate('#1e293b', '#6d28d9'));

    const svg = d3.select('#' + containerId)
        .append('svg')
        .attr('width', '100%')
        .attr('viewBox', `0 0 ${W} ${H}`)
        .attr('preserveAspectRatio', 'xMidYMid meet');

    const g = svg.append('g').attr('transform', `translate(${marginLeft},${marginTop})`);

    // Column labels (to-state)
    svg.selectAll('.col-label')
        .data(labels)
        .enter().append('text')
        .attr('class', 'col-label')
        .attr('x', (_, i) => marginLeft + i * cellSize + cellSize / 2)
        .attr('y', marginTop - 6)
        .attr('text-anchor', 'middle')
        .attr('fill', '#9ca3af')
        .attr('font-size', '9')
        .text(d => d.length > 7 ? d.slice(0, 6) + '…' : d);

    // Row labels (from-state)
    svg.selectAll('.row-label')
        .data(labels)
        .enter().append('text')
        .attr('class', 'row-label')
        .attr('x', marginLeft - 6)
        .attr('y', (_, i) => marginTop + i * cellSize + cellSize / 2 + 4)
        .attr('text-anchor', 'end')
        .attr('fill', '#9ca3af')
        .attr('font-size', '9')
        .text(d => d.length > 8 ? d.slice(0, 7) + '…' : d);

    // Build lookup map
    const probMap = {};
    cells.forEach(c => { probMap[`${c.from},${c.to}`] = c.prob; });

    // Cells
    for (let fr = 0; fr < n; fr++) {
        for (let to = 0; to < n; to++) {
            const prob = probMap[`${fr},${to}`] ?? 0;
            g.append('rect')
                .attr('x', to * cellSize)
                .attr('y', fr * cellSize)
                .attr('width', cellSize - 1)
                .attr('height', cellSize - 1)
                .attr('rx', 2)
                .attr('fill', colorScale(prob));

            if (prob > 0.05) {
                g.append('text')
                    .attr('x', to * cellSize + cellSize / 2)
                    .attr('y', fr * cellSize + cellSize / 2 + 4)
                    .attr('text-anchor', 'middle')
                    .attr('fill', prob > 0.5 ? '#e5e7eb' : '#94a3b8')
                    .attr('font-size', cellSize > 40 ? '10' : '8')
                    .text(prob.toFixed(2));
            }
        }
    }
}

// ── Inject chain data and render ─────────────────────────────────────────────
const peerStates    = <?= json_encode($peerStates) ?>;
const userStates    = <?= json_encode($userStates) ?>;
const torrentStates = <?= json_encode($torrentStates) ?>;
const matrixPeer    = <?= json_encode($matrixPeer) ?>;
const matrixUser    = <?= json_encode($matrixUser) ?>;
const matrixTorrent = <?= json_encode($matrixTorrent) ?>;

document.addEventListener('DOMContentLoaded', () => {
    if (matrixPeer.length)    renderHeatmap('heatmap-peer',    matrixPeer,    peerStates);
    if (matrixUser.length)    renderHeatmap('heatmap-user',    matrixUser,    userStates);
    if (matrixTorrent.length) renderHeatmap('heatmap-torrent', matrixTorrent, torrentStates);
});

// ── Torrent predictor Alpine component ───────────────────────────────────────
function torrentLookup() {
    return {
        torrentId: '',
        result: null,
        error: '',
        loading: false,
        async lookup() {
            const id = parseInt(this.torrentId);
            if (!id || id <= 0) { this.error = 'Enter a valid torrent ID'; return; }
            this.loading = true;
            this.error = '';
            this.result = null;
            try {
                const r = await fetch('api/markov-proxy.php?path=/torrent/' + id);
                if (r.status === 404) { this.error = 'Torrent not tracked by Markov engine'; return; }
                if (!r.ok) { this.error = `HTTP ${r.status}`; return; }
                this.result = await r.json();
            } catch (e) {
                this.error = 'Sidecar unreachable: ' + e.message;
            } finally {
                this.loading = false;
            }
        },
        stateColor(state) {
            const map = {
                THRIVING: 'text-green-400', HEALTHY: 'text-green-300',
                AT_RISK: 'text-yellow-400', DYING: 'text-orange-400', DEAD: 'text-red-400'
            };
            return map[state] ?? 'text-gray-300';
        }
    };
}
</script>

<?php include 'includes/footer.php'; ?>
