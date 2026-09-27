<?php
require_once 'config.php';
requireAuth();

$pageTitle = 'Credit Flow';

// ── Query ledger aggregates from the current DB shard ─────────────────────
$flows    = [];
$topUsers = [];
$summary  = ['total_charged' => 0, 'total_credited' => 0, 'net_flow' => 0, 'txn_count' => 0];

try {
    $db = OcelotDB::connect();

    // Aggregate credit flows: reason → resource_type → total charge (CC milliunits)
    $rows = $db->query("
        SELECT alloc_reason, resource_type,
               SUM(CASE WHEN charge > 0 THEN charge ELSE 0 END) AS charged,
               SUM(CASE WHEN charge < 0 THEN ABS(charge) ELSE 0 END) AS credited,
               COUNT(*) AS txn_count
        FROM commons_ledger
        WHERE created_at > " . (time() - 86400 * 7) . "
        GROUP BY alloc_reason, resource_type
        ORDER BY ABS(charged + credited) DESC
        LIMIT 50
    ")->fetchAll();

    foreach ($rows as $row) {
        $flows[] = $row;
    }

    // Summary totals
    $tot = $db->query("
        SELECT
            SUM(CASE WHEN charge > 0 THEN charge ELSE 0 END) AS total_charged,
            SUM(CASE WHEN charge < 0 THEN ABS(charge) ELSE 0 END) AS total_credited,
            COUNT(*) AS txn_count
        FROM commons_ledger
        WHERE created_at > " . (time() - 86400 * 7) . "
    ")->fetch();
    if ($tot) {
        $summary['total_charged']  = (int)($tot['total_charged']  ?? 0);
        $summary['total_credited'] = (int)($tot['total_credited'] ?? 0);
        $summary['txn_count']      = (int)($tot['txn_count']      ?? 0);
        $summary['net_flow']       = $summary['total_charged'] - $summary['total_credited'];
    }

    // Top 10 users by net spending (balance delta)
    $topUsers = $db->query("
        SELECT account_id, SUM(charge) AS net_charge, COUNT(*) AS txn_count
        FROM commons_ledger
        WHERE created_at > " . (time() - 86400 * 7) . "
        GROUP BY account_id
        ORDER BY SUM(charge) DESC
        LIMIT 10
    ")->fetchAll();

} catch (Exception $e) {
    $flows    = [];
    $topUsers = [];
    $errMsg = $e->getMessage();
}

// ── Build Sankey nodes + links for D3 ────────────────────────────────────
// Node naming: reasons on the left, resource types on the right
$reasonLabels = [
    'download_charge'       => 'Download',
    'upload_credit'         => 'Upload Credit',
    'seeding_credit'        => 'Seeding Credit',
    'storage_charge'        => 'Storage',
    'network_charge'        => 'Network',
    'initial_allocation'    => 'Initial Alloc',
    'admin_topup'           => 'Admin Topup',
    'reservation_hoarding'  => 'Hoarding Penalty',
    'abandoned_reservation' => 'Abandon Penalty',
    'chronic_over_requesting'=> 'Over-Request',
    'reputation_bonus'      => 'Rep Bonus',
];

$nodeIndex = [];
$sankeyNodes = [];
$sankeyLinks = [];

function nodeIdx(string $name, array &$nodes, array &$idx): int {
    if (!isset($idx[$name])) {
        $idx[$name] = count($nodes);
        $nodes[] = ['name' => $name];
    }
    return $idx[$name];
}

foreach ($flows as $row) {
    $reason  = $reasonLabels[$row['alloc_reason']] ?? $row['alloc_reason'];
    $resType = ucfirst(strtolower($row['resource_type']));
    $value   = (int)$row['charged'] + (int)$row['credited'];
    if ($value <= 0) continue;

    $from = nodeIdx($reason,  $sankeyNodes, $nodeIndex);
    $to   = nodeIdx($resType, $sankeyNodes, $nodeIndex);
    $sankeyLinks[] = ['source' => $from, 'target' => $to, 'value' => $value];
}

$sankeyJson = json_encode(['nodes' => $sankeyNodes, 'links' => $sankeyLinks], JSON_UNESCAPED_UNICODE);
$summaryJson = json_encode($summary);
$topUsersJson = json_encode($topUsers);

include 'includes/header.php';
?>

<div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-6">
    <!-- Page header -->
    <div class="mb-6 flex items-center justify-between">
        <div>
            <h1 class="text-2xl font-bold text-white">Credit Flow</h1>
            <p class="mt-1 text-sm text-gray-400">Sankey diagram of CC charges and credits (last 7 days)</p>
        </div>
        <span class="text-xs text-gray-500">Refreshes on page load</span>
    </div>

    <?php if (isset($errMsg)): ?>
    <div class="mb-4 rounded-md bg-red-900/40 border border-red-700 px-4 py-3 text-sm text-red-300">
        Commons ledger unavailable: <?= htmlspecialchars($errMsg) ?>
    </div>
    <?php endif; ?>

    <!-- Summary tiles -->
    <div class="grid grid-cols-2 sm:grid-cols-4 gap-4 mb-6">
        <?php
        $tiles = [
            ['label' => 'Total Charged',  'key' => 'total_charged',  'color' => 'text-red-400'],
            ['label' => 'Total Credited', 'key' => 'total_credited', 'color' => 'text-green-400'],
            ['label' => 'Net Flow',       'key' => 'net_flow',       'color' => 'text-blue-400'],
            ['label' => 'Transactions',   'key' => 'txn_count',      'color' => 'text-purple-400'],
        ];
        foreach ($tiles as $t): ?>
        <div class="rounded-lg bg-gray-800 border border-gray-700 p-4">
            <div class="text-xs text-gray-400 mb-1"><?= $t['label'] ?></div>
            <div class="text-xl font-mono font-semibold <?= $t['color'] ?>">
                <?= number_format($summary[$t['key']]) ?>
            </div>
            <div class="text-xs text-gray-500">CC milliunits</div>
        </div>
        <?php endforeach; ?>
    </div>

    <!-- Sankey diagram -->
    <div class="rounded-lg bg-gray-800 border border-gray-700 p-4 mb-6">
        <h2 class="text-sm font-semibold text-gray-300 mb-3">Credit Flow — Reason → Resource</h2>
        <?php if (empty($sankeyNodes)): ?>
            <div class="flex items-center justify-center h-48 text-gray-500 text-sm">
                No ledger data for the past 7 days
            </div>
        <?php else: ?>
            <div id="sankey-container" style="width:100%;height:420px;"></div>
        <?php endif; ?>
    </div>

    <!-- Top users table -->
    <div class="rounded-lg bg-gray-800 border border-gray-700 overflow-hidden">
        <div class="px-4 py-3 border-b border-gray-700">
            <h2 class="text-sm font-semibold text-gray-300">Top Users by Net Charge (7d)</h2>
        </div>
        <table class="min-w-full divide-y divide-gray-700 text-sm">
            <thead class="bg-gray-750">
                <tr>
                    <th class="px-4 py-2 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">User ID</th>
                    <th class="px-4 py-2 text-right text-xs font-medium text-gray-400 uppercase tracking-wider">Net Charge (CC)</th>
                    <th class="px-4 py-2 text-right text-xs font-medium text-gray-400 uppercase tracking-wider">Transactions</th>
                </tr>
            </thead>
            <tbody class="divide-y divide-gray-700">
                <?php foreach ($topUsers as $u): ?>
                <tr class="hover:bg-gray-700/40 transition-colors">
                    <td class="px-4 py-2 font-mono text-gray-200"><?= (int)$u['account_id'] ?></td>
                    <td class="px-4 py-2 text-right font-mono <?= $u['net_charge'] > 0 ? 'text-red-400' : 'text-green-400' ?>">
                        <?= number_format((int)$u['net_charge']) ?>
                    </td>
                    <td class="px-4 py-2 text-right text-gray-400"><?= (int)$u['txn_count'] ?></td>
                </tr>
                <?php endforeach; ?>
                <?php if (empty($topUsers)): ?>
                <tr><td colspan="3" class="px-4 py-4 text-center text-gray-500">No data</td></tr>
                <?php endif; ?>
            </tbody>
        </table>
    </div>
</div>

<script src="https://cdn.jsdelivr.net/npm/d3-sankey@0.12.3/dist/d3-sankey.min.js"></script>
<script>
(function () {
    const raw = <?= $sankeyJson ?>;
    if (!raw.nodes || raw.nodes.length === 0) return;

    const container = document.getElementById('sankey-container');
    if (!container) return;

    const W = container.clientWidth || 760;
    const H = 420;
    const margin = { top: 10, right: 160, bottom: 10, left: 160 };

    const svg = d3.select('#sankey-container')
        .append('svg')
        .attr('width', W)
        .attr('height', H)
        .attr('viewBox', `0 0 ${W} ${H}`)
        .style('width', '100%');

    const sankey = d3.sankey()
        .nodeWidth(18)
        .nodePadding(12)
        .extent([[margin.left, margin.top], [W - margin.right, H - margin.bottom]]);

    const graph = sankey({
        nodes: raw.nodes.map(d => Object.assign({}, d)),
        links: raw.links.map(d => Object.assign({}, d)),
    });

    const nodeColor = d3.scaleOrdinal(d3.schemeTableau10);

    // Links
    svg.append('g')
        .attr('fill', 'none')
        .selectAll('path')
        .data(graph.links)
        .join('path')
        .attr('d', d3.sankeyLinkHorizontal())
        .attr('stroke', d => nodeColor(d.source.name))
        .attr('stroke-width', d => Math.max(1, d.width))
        .attr('opacity', 0.45)
        .on('mouseover', function () { d3.select(this).attr('opacity', 0.75); })
        .on('mouseout',  function () { d3.select(this).attr('opacity', 0.45); })
        .append('title')
        .text(d => `${d.source.name} → ${d.target.name}\n${d.value.toLocaleString()} CC`);

    // Nodes
    const nodeG = svg.append('g')
        .selectAll('rect')
        .data(graph.nodes)
        .join('rect')
        .attr('x', d => d.x0)
        .attr('y', d => d.y0)
        .attr('width', d => d.x1 - d.x0)
        .attr('height', d => Math.max(1, d.y1 - d.y0))
        .attr('fill', d => nodeColor(d.name))
        .attr('rx', 3)
        .append('title')
        .text(d => `${d.name}\n${d.value.toLocaleString()} CC`);

    // Labels
    svg.append('g')
        .style('font', '11px monospace')
        .style('fill', '#d1d5db')
        .selectAll('text')
        .data(graph.nodes)
        .join('text')
        .attr('x', d => d.x0 < W / 2 ? d.x1 + 6 : d.x0 - 6)
        .attr('y', d => (d.y0 + d.y1) / 2)
        .attr('dy', '0.35em')
        .attr('text-anchor', d => d.x0 < W / 2 ? 'start' : 'end')
        .text(d => d.name);
})();
</script>

<?php include 'includes/footer.php'; ?>
