<?php
require_once 'config.php';
requireAuth();

$pageTitle = 'Dashboard';

// Fetch live tracker stats (includes circuit breaker state)
$trackerStats  = TrackerAPI::getStats();
$cbState       = $trackerStats['circuit_breaker_state'] ?? null;
$trackerUptime = isset($trackerStats['uptime_seconds']) ? (int)$trackerStats['uptime_seconds'] : null;

// --- Database queries, isolated so one failure doesn't zero everything ---
$recentPeers    = ['total' => 0, 'seeders' => 0, 'leechers' => 0];
$announceStats  = ['total_announces' => 0, 'active_users' => 0, 'active_torrents' => 0];
$topUploaders   = [];
$recentSnatches = [];
$activityBuckets = [];
$dbError = '';

try {
    $db = OcelotDB::connect();

    // Peer counts (active in last 2 h)
    $recentPeers = $db->query("
        SELECT COUNT(*) as total,
               SUM(CASE WHEN torrent_left = 0 THEN 1 ELSE 0 END) as seeders,
               SUM(CASE WHEN torrent_left > 0 THEN 1 ELSE 0 END) as leechers
        FROM peers
        WHERE timestamp > " . (time() - 7200)
    )->fetch();

    // Announce / user / torrent counts (last 1 h)
    $announceStats = $db->query("
        SELECT COUNT(*) as total_announces,
               COUNT(DISTINCT user_id) as active_users,
               COUNT(DISTINCT torrent_id) as active_torrents
        FROM peers
        WHERE timestamp > " . (time() - 3600)
    )->fetch();

    // Top uploaders — from users table (id, uploaded, downloaded)
    $topUploaders = $db->query("
        SELECT id as user_id, uploaded as total_uploaded
        FROM users
        WHERE uploaded > 0
        ORDER BY uploaded DESC
        LIMIT 10
    ")->fetchAll();

    // Recent snatches
    $recentSnatches = $db->query("
        SELECT torrent_id, user_id, snatched_time as timestamp
        FROM snatches
        ORDER BY snatched_time DESC
        LIMIT 20
    ")->fetchAll();

    // 5-minute announce buckets for last 1 h (12 data points)
    $activityBuckets = $db->query("
        SELECT (timestamp / 300) * 300 as bucket,
               COUNT(*) as announces
        FROM peers
        WHERE timestamp > " . (time() - 3600) . "
        GROUP BY bucket
        ORDER BY bucket
    ")->fetchAll();

} catch (Exception $e) {
    $dbError = $e->getMessage();
}

// Fill gaps so chart always has 12 evenly-spaced points
$now       = time();
$bucketMap = [];
foreach ($activityBuckets as $b) {
    $bucketMap[(int)$b['bucket']] = (int)$b['announces'];
}
$chartPoints = [];
for ($i = 11; $i >= 0; $i--) {
    $ts = (int)(floor(($now - $i * 300) / 300) * 300);
    $chartPoints[] = ['ts' => $ts, 'label' => date('H:i', $ts), 'announces' => $bucketMap[$ts] ?? 0];
}

include 'includes/header.php';
?>

<?php if ($dbError): ?>
<div class="bg-red-900 border border-red-700 rounded-lg p-4 mb-6 flex items-center gap-3">
    <i data-lucide="alert-circle" class="w-5 h-5 text-red-400 flex-shrink-0"></i>
    <p class="text-sm text-red-200">Database error: <?= htmlspecialchars($dbError) ?></p>
</div>
<?php endif; ?>

<!-- Tracker Status Bar -->
<div class="flex flex-wrap gap-3 mb-6">
    <?php if ($cbState !== null):
        $cbColor = match($cbState) {
            'CLOSED'    => 'bg-green-900 border-green-700 text-green-300',
            'HALF_OPEN' => 'bg-yellow-900 border-yellow-700 text-yellow-300',
            'OPEN'      => 'bg-red-900 border-red-700 text-red-300',
            default     => 'bg-gray-800 border-gray-600 text-gray-300',
        };
        $cbIcon = match($cbState) {
            'CLOSED'    => 'shield-check',
            'HALF_OPEN' => 'shield-alert',
            'OPEN'      => 'shield-off',
            default     => 'shield',
        };
    ?>
    <div class="flex items-center gap-2 px-3 py-2 rounded-md border text-sm font-medium <?= $cbColor ?>">
        <i data-lucide="<?= $cbIcon ?>" class="w-4 h-4"></i>
        Circuit Breaker: <?= htmlspecialchars($cbState) ?>
    </div>
    <?php elseif ($trackerStats === null): ?>
    <div class="flex items-center gap-2 px-3 py-2 rounded-md border bg-red-900 border-red-700 text-red-300 text-sm">
        <i data-lucide="wifi-off" class="w-4 h-4"></i>
        Tracker API offline
    </div>
    <?php endif; ?>

    <?php if ($trackerUptime !== null):
        $h = floor($trackerUptime / 3600);
        $m = floor(($trackerUptime % 3600) / 60);
    ?>
    <div class="flex items-center gap-2 px-3 py-2 rounded-md border bg-gray-800 border-gray-700 text-gray-300 text-sm">
        <i data-lucide="clock" class="w-4 h-4"></i>
        Uptime: <?= $h ?>h <?= $m ?>m
    </div>
    <?php endif; ?>

    <?php if (!empty($trackerStats['announcements'])): ?>
    <div class="flex items-center gap-2 px-3 py-2 rounded-md border bg-gray-800 border-gray-700 text-gray-300 text-sm">
        <i data-lucide="zap" class="w-4 h-4"></i>
        <?= number_format($trackerStats['announcements']) ?> total announces
    </div>
    <?php endif; ?>

    <?php if (!empty($trackerStats['num_torrents'])): ?>
    <div class="flex items-center gap-2 px-3 py-2 rounded-md border bg-gray-800 border-gray-700 text-gray-300 text-sm">
        <i data-lucide="disc" class="w-4 h-4"></i>
        <?= number_format($trackerStats['num_torrents']) ?> torrents loaded
    </div>
    <?php endif; ?>
</div>

<!-- Stats Grid -->
<div class="grid grid-cols-2 lg:grid-cols-4 gap-4 mb-6">
    <div class="bg-gray-800 rounded-lg border border-gray-700 p-5">
        <div class="flex items-center gap-3">
            <i data-lucide="users" class="w-7 h-7 text-blue-400 flex-shrink-0"></i>
            <div>
                <p class="text-xs text-gray-400">Total Peers</p>
                <p class="text-2xl font-bold text-white"><?= number_format($recentPeers['total']) ?></p>
                <p class="text-xs text-gray-600">active 2 h</p>
            </div>
        </div>
    </div>
    <div class="bg-gray-800 rounded-lg border border-gray-700 p-5">
        <div class="flex items-center gap-3">
            <i data-lucide="arrow-up-circle" class="w-7 h-7 text-green-400 flex-shrink-0"></i>
            <div>
                <p class="text-xs text-gray-400">Seeders</p>
                <p class="text-2xl font-bold text-white"><?= number_format($recentPeers['seeders']) ?></p>
                <p class="text-xs text-gray-600">active 2 h</p>
            </div>
        </div>
    </div>
    <div class="bg-gray-800 rounded-lg border border-gray-700 p-5">
        <div class="flex items-center gap-3">
            <i data-lucide="arrow-down-circle" class="w-7 h-7 text-yellow-400 flex-shrink-0"></i>
            <div>
                <p class="text-xs text-gray-400">Leechers</p>
                <p class="text-2xl font-bold text-white"><?= number_format($recentPeers['leechers']) ?></p>
                <p class="text-xs text-gray-600">active 2 h</p>
            </div>
        </div>
    </div>
    <div class="bg-gray-800 rounded-lg border border-gray-700 p-5">
        <div class="flex items-center gap-3">
            <i data-lucide="activity" class="w-7 h-7 text-purple-400 flex-shrink-0"></i>
            <div>
                <p class="text-xs text-gray-400">Announces</p>
                <p class="text-2xl font-bold text-white"><?= number_format($announceStats['total_announces']) ?></p>
                <p class="text-xs text-gray-600">last 1 h</p>
            </div>
        </div>
    </div>
</div>

<!-- Second row of stats -->
<div class="grid grid-cols-2 lg:grid-cols-4 gap-4 mb-6">
    <div class="bg-gray-800 rounded-lg border border-gray-700 p-5">
        <div class="flex items-center gap-3">
            <i data-lucide="user-check" class="w-7 h-7 text-cyan-400 flex-shrink-0"></i>
            <div>
                <p class="text-xs text-gray-400">Active Users</p>
                <p class="text-2xl font-bold text-white"><?= number_format($announceStats['active_users']) ?></p>
                <p class="text-xs text-gray-600">last 1 h</p>
            </div>
        </div>
    </div>
    <div class="bg-gray-800 rounded-lg border border-gray-700 p-5">
        <div class="flex items-center gap-3">
            <i data-lucide="disc" class="w-7 h-7 text-indigo-400 flex-shrink-0"></i>
            <div>
                <p class="text-xs text-gray-400">Active Torrents</p>
                <p class="text-2xl font-bold text-white"><?= number_format($announceStats['active_torrents']) ?></p>
                <p class="text-xs text-gray-600">last 1 h</p>
            </div>
        </div>
    </div>
    <?php
    $totalUp = array_sum(array_column($topUploaders, 'total_uploaded'));
    $shards  = [];
    try { $shards = OcelotDB::getAllShards(); } catch (Exception $e) {}
    $totalShardSize = array_sum(array_map('filesize', $shards));
    ?>
    <div class="bg-gray-800 rounded-lg border border-gray-700 p-5">
        <div class="flex items-center gap-3">
            <i data-lucide="upload" class="w-7 h-7 text-green-400 flex-shrink-0"></i>
            <div>
                <p class="text-xs text-gray-400">Total Uploaded</p>
                <p class="text-2xl font-bold text-white"><?= formatBytes($totalUp) ?></p>
                <p class="text-xs text-gray-600">all time</p>
            </div>
        </div>
    </div>
    <div class="bg-gray-800 rounded-lg border border-gray-700 p-5">
        <div class="flex items-center gap-3">
            <i data-lucide="database" class="w-7 h-7 text-orange-400 flex-shrink-0"></i>
            <div>
                <p class="text-xs text-gray-400">DB Size</p>
                <p class="text-2xl font-bold text-white"><?= formatBytes($totalShardSize) ?></p>
                <p class="text-xs text-gray-600"><?= count($shards) ?> shard<?= count($shards) !== 1 ? 's' : '' ?></p>
            </div>
        </div>
    </div>
</div>

<!-- Charts Row -->
<div class="grid grid-cols-1 lg:grid-cols-2 gap-6 mb-6">
    <!-- Peer Distribution -->
    <div class="bg-gray-800 border border-gray-700 rounded-lg p-5">
        <h3 class="text-base font-semibold text-white mb-4">Peer Distribution</h3>
        <div id="peerChart" style="height:220px;"></div>
    </div>

    <!-- 5-min Activity Buckets -->
    <div class="bg-gray-800 border border-gray-700 rounded-lg p-5">
        <h3 class="text-base font-semibold text-white mb-4 flex items-center gap-2">
            Announce Activity
            <span class="text-xs text-gray-500 font-normal">last 60 min · 5 min buckets</span>
        </h3>
        <div id="activityChart" style="height:220px;"></div>
    </div>
</div>

<!-- Tables Row -->
<div class="grid grid-cols-1 lg:grid-cols-2 gap-6">
    <!-- Top Uploaders -->
    <div class="bg-gray-800 border border-gray-700 rounded-lg overflow-hidden">
        <div class="px-5 py-4 border-b border-gray-700 flex items-center gap-2">
            <i data-lucide="trophy" class="w-4 h-4 text-yellow-400"></i>
            <h3 class="text-sm font-semibold text-white">Top Uploaders</h3>
        </div>
        <table class="min-w-full divide-y divide-gray-700 text-sm">
            <thead class="bg-gray-900">
                <tr>
                    <th class="px-5 py-3 text-left text-xs text-gray-400 uppercase">#</th>
                    <th class="px-5 py-3 text-left text-xs text-gray-400 uppercase">User</th>
                    <th class="px-5 py-3 text-left text-xs text-gray-400 uppercase">Uploaded</th>
                </tr>
            </thead>
            <tbody class="divide-y divide-gray-700">
                <?php if (empty($topUploaders)): ?>
                <tr><td colspan="3" class="px-5 py-6 text-center text-gray-500">No data</td></tr>
                <?php else: foreach ($topUploaders as $i => $u): ?>
                <tr class="hover:bg-gray-750">
                    <td class="px-5 py-3 text-gray-500 text-xs"><?= $i + 1 ?></td>
                    <td class="px-5 py-3 text-blue-400 font-mono"><?= $u['user_id'] ?></td>
                    <td class="px-5 py-3 text-green-400"><?= formatBytes($u['total_uploaded']) ?></td>
                </tr>
                <?php endforeach; endif; ?>
            </tbody>
        </table>
    </div>

    <!-- Recent Snatches -->
    <div class="bg-gray-800 border border-gray-700 rounded-lg overflow-hidden">
        <div class="px-5 py-4 border-b border-gray-700 flex items-center gap-2">
            <i data-lucide="download" class="w-4 h-4 text-purple-400"></i>
            <h3 class="text-sm font-semibold text-white">Recent Snatches</h3>
        </div>
        <table class="min-w-full divide-y divide-gray-700 text-sm">
            <thead class="bg-gray-900">
                <tr>
                    <th class="px-5 py-3 text-left text-xs text-gray-400 uppercase">Torrent</th>
                    <th class="px-5 py-3 text-left text-xs text-gray-400 uppercase">User</th>
                    <th class="px-5 py-3 text-left text-xs text-gray-400 uppercase">When</th>
                </tr>
            </thead>
            <tbody class="divide-y divide-gray-700">
                <?php if (empty($recentSnatches)): ?>
                <tr><td colspan="3" class="px-5 py-6 text-center text-gray-500">No snatches yet</td></tr>
                <?php else: foreach (array_slice($recentSnatches, 0, 15) as $s): ?>
                <tr class="hover:bg-gray-750">
                    <td class="px-5 py-3 text-white font-mono"><?= $s['torrent_id'] ?></td>
                    <td class="px-5 py-3 text-blue-400"><?= $s['user_id'] ?></td>
                    <td class="px-5 py-3 text-gray-400 text-xs"><?= timeAgo($s['timestamp']) ?></td>
                </tr>
                <?php endforeach; endif; ?>
            </tbody>
        </table>
    </div>
</div>

<script>
// ---- Peer Distribution Donut ----
(function() {
    const seeders  = <?= (int)$recentPeers['seeders'] ?>;
    const leechers = <?= (int)$recentPeers['leechers'] ?>;
    const total    = seeders + leechers;

    const el = document.getElementById('peerChart');
    const w  = el.clientWidth;
    const h  = 220;
    const r  = Math.min(w, h) / 2 - 10;

    const svg = d3.select(el).append('svg')
        .attr('width', w).attr('height', h)
        .append('g').attr('transform', `translate(${w/2},${h/2})`);

    if (total === 0) {
        svg.append('text').attr('text-anchor','middle').attr('fill','#6b7280')
           .attr('dy','0.35em').text('No active peers');
        return;
    }

    const data = [
        { label: 'Seeders',  value: seeders,  color: '#4ade80' },
        { label: 'Leechers', value: leechers, color: '#fbbf24' }
    ];

    const pie  = d3.pie().sort(null).value(d => d.value);
    const arc  = d3.arc().innerRadius(r * 0.55).outerRadius(r);
    const arcs = svg.selectAll('g').data(pie(data)).enter().append('g');

    arcs.append('path')
        .attr('d', arc)
        .attr('fill', d => d.data.color)
        .attr('stroke', '#1f2937').attr('stroke-width', 2);

    // Center label
    svg.append('text').attr('text-anchor','middle').attr('fill','white')
       .attr('font-size','22px').attr('font-weight','bold').attr('dy','-0.1em')
       .text(total.toLocaleString());
    svg.append('text').attr('text-anchor','middle').attr('fill','#9ca3af')
       .attr('font-size','11px').attr('dy','1.4em').text('peers');

    // Legend
    const legend = d3.select(el).select('svg').append('g')
        .attr('transform', `translate(10, ${h - 40})`);
    data.forEach((d, i) => {
        const g = legend.append('g').attr('transform', `translate(${i * 110}, 0)`);
        g.append('rect').attr('width', 10).attr('height', 10).attr('rx', 2).attr('fill', d.color);
        g.append('text').attr('x', 14).attr('y', 9).attr('fill', '#d1d5db')
         .attr('font-size', '11px')
         .text(`${d.label}: ${d.value}`);
    });
})();

// ---- Announce Activity Bar Chart ----
(function() {
    const data = <?= json_encode($chartPoints) ?>;
    const el   = document.getElementById('activityChart');
    const margin = { top: 10, right: 20, bottom: 30, left: 45 };
    const w = el.clientWidth - margin.left - margin.right;
    const h = 220 - margin.top - margin.bottom;

    const svg = d3.select(el).append('svg')
        .attr('width',  w + margin.left + margin.right)
        .attr('height', h + margin.top  + margin.bottom)
        .append('g').attr('transform', `translate(${margin.left},${margin.top})`);

    const x = d3.scaleBand()
        .domain(data.map(d => d.label))
        .range([0, w]).padding(0.25);

    const maxVal = d3.max(data, d => d.announces) || 10;
    const y = d3.scaleLinear().domain([0, maxVal]).nice().range([h, 0]);

    // Grid lines
    svg.append('g').call(
        d3.axisLeft(y).tickSize(-w).tickFormat('')
    ).selectAll('line').attr('stroke','#374151').attr('stroke-dasharray','2,2');
    svg.select('.domain').remove();

    // Axes
    svg.append('g').attr('transform', `translate(0,${h})`)
       .call(d3.axisBottom(x).tickValues(data.filter((_,i) => i % 3 === 0).map(d => d.label)))
       .selectAll('text').attr('fill','#9ca3af').attr('font-size','10px');
    svg.append('g').call(d3.axisLeft(y).ticks(4))
       .selectAll('text').attr('fill','#9ca3af').attr('font-size','10px');
    svg.selectAll('.domain').attr('stroke','#374151');

    // Bars
    svg.selectAll('.bar').data(data).enter().append('rect')
        .attr('class','bar')
        .attr('x', d => x(d.label))
        .attr('y', d => y(d.announces))
        .attr('width', x.bandwidth())
        .attr('height', d => h - y(d.announces))
        .attr('fill', '#8b5cf6').attr('rx', 2).attr('opacity', 0.85)
        .on('mouseover', function(event, d) {
            d3.select(this).attr('opacity', 1);
            tooltip.style('display','block')
                   .html(`<span class="font-mono text-xs">${d.label}</span><br>${d.announces} announces`)
                   .style('left', (event.pageX + 8) + 'px')
                   .style('top',  (event.pageY - 28) + 'px');
        })
        .on('mouseout', function() {
            d3.select(this).attr('opacity', 0.85);
            tooltip.style('display','none');
        });

    const tooltip = d3.select('body').append('div')
        .style('position','absolute').style('display','none')
        .style('background','#1f2937').style('border','1px solid #374151')
        .style('color','#f3f4f6').style('padding','6px 10px')
        .style('border-radius','6px').style('pointer-events','none')
        .style('font-size','12px').style('z-index','100');
})();
</script>

<?php include 'includes/footer.php'; ?>
