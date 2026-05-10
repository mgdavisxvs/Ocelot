<?php
require_once 'config.php';
requireAuth();

$pageTitle = 'Dashboard';

// Fetch recent statistics from database
try {
    $db = OcelotDB::connect();

    // Get peer counts from most recent records
    $recentPeers = $db->query("
        SELECT COUNT(*) as total,
               SUM(CASE WHEN torrent_left = 0 THEN 1 ELSE 0 END) as seeders,
               SUM(CASE WHEN torrent_left > 0 THEN 1 ELSE 0 END) as leechers
        FROM peers
        WHERE timestamp > " . (time() - 7200) . "
    ")->fetch();

    // Get announce counts
    $announceStats = $db->query("
        SELECT COUNT(*) as total_announces,
               COUNT(DISTINCT user_id) as active_users,
               COUNT(DISTINCT torrent_id) as active_torrents
        FROM peers
        WHERE timestamp > " . (time() - 3600) . "
    ")->fetch();

    // Get top uploaders
    $topUploaders = $db->query("
        SELECT user_id, SUM(uploaded) as total_uploaded
        FROM user_stats
        GROUP BY user_id
        ORDER BY total_uploaded DESC
        LIMIT 10
    ")->fetchAll();

    // Get recent snatches
    $recentSnatches = $db->query("
        SELECT torrent_id, user_id, timestamp, ip
        FROM snatches
        ORDER BY timestamp DESC
        LIMIT 20
    ")->fetchAll();

} catch (Exception $e) {
    $error = $e->getMessage();
    $recentPeers = ['total' => 0, 'seeders' => 0, 'leechers' => 0];
    $announceStats = ['total_announces' => 0, 'active_users' => 0, 'active_torrents' => 0];
    $topUploaders = [];
    $recentSnatches = [];
}

include 'includes/header.php';
?>

<?php if (isset($error)): ?>
<div class="rounded-md bg-red-900 border border-red-700 p-4 mb-6">
    <div class="flex">
        <i data-lucide="alert-circle" class="h-5 w-5 text-red-400"></i>
        <div class="ml-3">
            <p class="text-sm text-red-200">Database Error: <?= htmlspecialchars($error) ?></p>
        </div>
    </div>
</div>
<?php endif; ?>

<!-- Stats Grid -->
<div class="grid grid-cols-1 gap-6 sm:grid-cols-2 lg:grid-cols-4 mb-8">
    <!-- Total Peers -->
    <div class="bg-gray-800 overflow-hidden shadow rounded-lg border border-gray-700">
        <div class="p-5">
            <div class="flex items-center">
                <div class="flex-shrink-0">
                    <i data-lucide="users" class="h-8 w-8 text-blue-400"></i>
                </div>
                <div class="ml-5 w-0 flex-1">
                    <dl>
                        <dt class="text-sm font-medium text-gray-400 truncate">Total Peers</dt>
                        <dd class="text-3xl font-semibold text-white"><?= number_format($recentPeers['total']) ?></dd>
                    </dl>
                </div>
            </div>
        </div>
    </div>

    <!-- Seeders -->
    <div class="bg-gray-800 overflow-hidden shadow rounded-lg border border-gray-700">
        <div class="p-5">
            <div class="flex items-center">
                <div class="flex-shrink-0">
                    <i data-lucide="arrow-up" class="h-8 w-8 text-green-400"></i>
                </div>
                <div class="ml-5 w-0 flex-1">
                    <dl>
                        <dt class="text-sm font-medium text-gray-400 truncate">Seeders</dt>
                        <dd class="text-3xl font-semibold text-white"><?= number_format($recentPeers['seeders']) ?></dd>
                    </dl>
                </div>
            </div>
        </div>
    </div>

    <!-- Leechers -->
    <div class="bg-gray-800 overflow-hidden shadow rounded-lg border border-gray-700">
        <div class="p-5">
            <div class="flex items-center">
                <div class="flex-shrink-0">
                    <i data-lucide="arrow-down" class="h-8 w-8 text-yellow-400"></i>
                </div>
                <div class="ml-5 w-0 flex-1">
                    <dl>
                        <dt class="text-sm font-medium text-gray-400 truncate">Leechers</dt>
                        <dd class="text-3xl font-semibold text-white"><?= number_format($recentPeers['leechers']) ?></dd>
                    </dl>
                </div>
            </div>
        </div>
    </div>

    <!-- Announces (1h) -->
    <div class="bg-gray-800 overflow-hidden shadow rounded-lg border border-gray-700">
        <div class="p-5">
            <div class="flex items-center">
                <div class="flex-shrink-0">
                    <i data-lucide="activity" class="h-8 w-8 text-purple-400"></i>
                </div>
                <div class="ml-5 w-0 flex-1">
                    <dl>
                        <dt class="text-sm font-medium text-gray-400 truncate">Announces (1h)</dt>
                        <dd class="text-3xl font-semibold text-white"><?= number_format($announceStats['total_announces']) ?></dd>
                    </dl>
                </div>
            </div>
        </div>
    </div>
</div>

<!-- Charts Row -->
<div class="grid grid-cols-1 gap-6 lg:grid-cols-2 mb-8">
    <!-- Peer Distribution Chart -->
    <div class="bg-gray-800 shadow rounded-lg border border-gray-700 p-6">
        <h3 class="text-lg font-medium text-white mb-4">Peer Distribution</h3>
        <div id="peerChart" class="h-64"></div>
    </div>

    <!-- Activity Timeline -->
    <div class="bg-gray-800 shadow rounded-lg border border-gray-700 p-6">
        <h3 class="text-lg font-medium text-white mb-4">Activity Timeline (Last Hour)</h3>
        <div id="activityChart" class="h-64"></div>
    </div>
</div>

<!-- Tables Row -->
<div class="grid grid-cols-1 gap-6 lg:grid-cols-2">
    <!-- Top Uploaders -->
    <div class="bg-gray-800 shadow rounded-lg border border-gray-700">
        <div class="px-4 py-5 sm:px-6 border-b border-gray-700">
            <h3 class="text-lg font-medium text-white">Top Uploaders</h3>
        </div>
        <div class="overflow-hidden">
            <table class="min-w-full divide-y divide-gray-700">
                <thead class="bg-gray-900">
                    <tr>
                        <th class="px-6 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">User ID</th>
                        <th class="px-6 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">Uploaded</th>
                    </tr>
                </thead>
                <tbody class="divide-y divide-gray-700">
                    <?php if (empty($topUploaders)): ?>
                    <tr>
                        <td colspan="2" class="px-6 py-4 text-sm text-gray-400 text-center">No data available</td>
                    </tr>
                    <?php else: ?>
                        <?php foreach ($topUploaders as $uploader): ?>
                        <tr>
                            <td class="px-6 py-4 whitespace-nowrap text-sm text-white"><?= $uploader['user_id'] ?></td>
                            <td class="px-6 py-4 whitespace-nowrap text-sm text-green-400"><?= formatBytes($uploader['total_uploaded']) ?></td>
                        </tr>
                        <?php endforeach; ?>
                    <?php endif; ?>
                </tbody>
            </table>
        </div>
    </div>

    <!-- Recent Snatches -->
    <div class="bg-gray-800 shadow rounded-lg border border-gray-700">
        <div class="px-4 py-5 sm:px-6 border-b border-gray-700">
            <h3 class="text-lg font-medium text-white">Recent Snatches</h3>
        </div>
        <div class="overflow-hidden">
            <table class="min-w-full divide-y divide-gray-700">
                <thead class="bg-gray-900">
                    <tr>
                        <th class="px-6 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">Torrent</th>
                        <th class="px-6 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">User</th>
                        <th class="px-6 py-3 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">Time</th>
                    </tr>
                </thead>
                <tbody class="divide-y divide-gray-700">
                    <?php if (empty($recentSnatches)): ?>
                    <tr>
                        <td colspan="3" class="px-6 py-4 text-sm text-gray-400 text-center">No snatches yet</td>
                    </tr>
                    <?php else: ?>
                        <?php foreach (array_slice($recentSnatches, 0, 10) as $snatch): ?>
                        <tr>
                            <td class="px-6 py-4 whitespace-nowrap text-sm text-white"><?= $snatch['torrent_id'] ?></td>
                            <td class="px-6 py-4 whitespace-nowrap text-sm text-blue-400"><?= $snatch['user_id'] ?></td>
                            <td class="px-6 py-4 whitespace-nowrap text-sm text-gray-400"><?= timeAgo($snatch['timestamp']) ?></td>
                        </tr>
                        <?php endforeach; ?>
                    <?php endif; ?>
                </tbody>
            </table>
        </div>
    </div>
</div>

<script>
// Peer Distribution Pie Chart
const peerData = [
    { label: 'Seeders', value: <?= $recentPeers['seeders'] ?>, color: '#4ade80' },
    { label: 'Leechers', value: <?= $recentPeers['leechers'] ?>, color: '#fbbf24' }
];

const width = document.getElementById('peerChart').clientWidth;
const height = 256;
const radius = Math.min(width, height) / 2;

const svg = d3.select('#peerChart')
    .append('svg')
    .attr('width', width)
    .attr('height', height)
    .append('g')
    .attr('transform', `translate(${width/2},${height/2})`);

const pie = d3.pie().value(d => d.value);
const arc = d3.arc().innerRadius(0).outerRadius(radius - 10);

const arcs = svg.selectAll('arc')
    .data(pie(peerData))
    .enter()
    .append('g');

arcs.append('path')
    .attr('d', arc)
    .attr('fill', d => d.data.color)
    .attr('stroke', '#1f2937')
    .attr('stroke-width', 2);

arcs.append('text')
    .attr('transform', d => `translate(${arc.centroid(d)})`)
    .attr('text-anchor', 'middle')
    .attr('fill', 'white')
    .attr('font-size', '14px')
    .text(d => d.data.value > 0 ? d.data.label : '');

// Activity Timeline (Mock data - replace with real data from API)
const activityData = Array.from({length: 12}, (_, i) => ({
    time: new Date(Date.now() - (11-i) * 5 * 60000),
    announces: Math.floor(Math.random() * 100) + 20
}));

const margin = {top: 10, right: 30, bottom: 30, left: 40};
const chartWidth = document.getElementById('activityChart').clientWidth - margin.left - margin.right;
const chartHeight = 256 - margin.top - margin.bottom;

const timelineSvg = d3.select('#activityChart')
    .append('svg')
    .attr('width', chartWidth + margin.left + margin.right)
    .attr('height', chartHeight + margin.top + margin.bottom)
    .append('g')
    .attr('transform', `translate(${margin.left},${margin.top})`);

const x = d3.scaleTime()
    .domain(d3.extent(activityData, d => d.time))
    .range([0, chartWidth]);

const y = d3.scaleLinear()
    .domain([0, d3.max(activityData, d => d.announces)])
    .range([chartHeight, 0]);

timelineSvg.append('g')
    .attr('transform', `translate(0,${chartHeight})`)
    .call(d3.axisBottom(x).ticks(6).tickFormat(d3.timeFormat('%H:%M')))
    .attr('color', '#9ca3af');

timelineSvg.append('g')
    .call(d3.axisLeft(y))
    .attr('color', '#9ca3af');

const line = d3.line()
    .x(d => x(d.time))
    .y(d => y(d.announces))
    .curve(d3.curveMonotoneX);

timelineSvg.append('path')
    .datum(activityData)
    .attr('fill', 'none')
    .attr('stroke', '#8b5cf6')
    .attr('stroke-width', 2)
    .attr('d', line);

timelineSvg.selectAll('dot')
    .data(activityData)
    .enter()
    .append('circle')
    .attr('cx', d => x(d.time))
    .attr('cy', d => y(d.announces))
    .attr('r', 3)
    .attr('fill', '#8b5cf6');
</script>

<?php include 'includes/footer.php'; ?>
