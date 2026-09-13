<?php
require_once 'config.php';
requireAuth();

$pageTitle = 'Statistics & Analytics';

// Fetch comprehensive statistics
try {
    $db = OcelotDB::connect();

    // Overall stats
    $overallStats = $db->query("
        SELECT
            COUNT(DISTINCT user_id) as total_users,
            COUNT(DISTINCT torrent_id) as total_torrents,
            COUNT(*) as total_peers,
            SUM(uploaded) as total_uploaded,
            SUM(downloaded) as total_downloaded
        FROM peers
        WHERE timestamp > " . (time() - 86400) . "
    ")->fetch();

    // Hourly activity for last 24 hours
    $hourlyActivity = [];
    for ($i = 23; $i >= 0; $i--) {
        $hourStart = time() - ($i * 3600);
        $hourEnd = $hourStart + 3600;

        $stats = $db->query("
            SELECT COUNT(*) as announces
            FROM peers
            WHERE timestamp BETWEEN $hourStart AND $hourEnd
        ")->fetch();

        $hourlyActivity[] = [
            'hour' => date('H:00', $hourStart),
            'timestamp' => $hourStart,
            'announces' => $stats['announces']
        ];
    }

    // Database shard information
    $shards = OcelotDB::getAllShards();
    $shardInfo = [];

    foreach ($shards as $shardPath) {
        $size = filesize($shardPath);
        $shardInfo[] = [
            'name' => basename($shardPath),
            'size' => $size,
            'sizeFmt' => formatBytes($size),
            'percentage' => ($size / (84 * 1024 * 1024 * 1024)) * 100
        ];
    }

    // Top torrents by activity
    $topTorrents = $db->query("
        SELECT torrent_id, COUNT(*) as announce_count
        FROM peers
        WHERE timestamp > " . (time() - 3600) . "
        GROUP BY torrent_id
        ORDER BY announce_count DESC
        LIMIT 10
    ")->fetchAll();

} catch (Exception $e) {
    $error = $e->getMessage();
    $overallStats = ['total_users' => 0, 'total_torrents' => 0, 'total_peers' => 0, 'total_uploaded' => 0, 'total_downloaded' => 0];
    $hourlyActivity = [];
    $shardInfo = [];
    $topTorrents = [];
}

include 'includes/header.php';
?>

<?php if (isset($error)): ?>
<div class="rounded-md bg-red-900 border border-red-700 p-4 mb-6">
    <div class="flex">
        <i data-lucide="alert-circle" class="h-5 w-5 text-red-400"></i>
        <div class="ml-3">
            <p class="text-sm text-red-200">Error: <?= htmlspecialchars($error) ?></p>
        </div>
    </div>
</div>
<?php endif; ?>

<!-- 24 Hour Overview -->
<div class="bg-gray-800 shadow rounded-lg border border-gray-700 p-6 mb-6">
    <h2 class="text-xl font-semibold text-white mb-4">24 Hour Overview</h2>
    <div class="grid grid-cols-2 md:grid-cols-5 gap-4">
        <div class="text-center">
            <div class="text-3xl font-bold text-blue-400"><?= number_format($overallStats['total_users']) ?></div>
            <div class="text-sm text-gray-400">Active Users</div>
        </div>
        <div class="text-center">
            <div class="text-3xl font-bold text-purple-400"><?= number_format($overallStats['total_torrents']) ?></div>
            <div class="text-sm text-gray-400">Active Torrents</div>
        </div>
        <div class="text-center">
            <div class="text-3xl font-bold text-white"><?= number_format($overallStats['total_peers']) ?></div>
            <div class="text-sm text-gray-400">Total Announces</div>
        </div>
        <div class="text-center">
            <div class="text-3xl font-bold text-green-400"><?= formatBytes($overallStats['total_uploaded']) ?></div>
            <div class="text-sm text-gray-400">Uploaded</div>
        </div>
        <div class="text-center">
            <div class="text-3xl font-bold text-yellow-400"><?= formatBytes($overallStats['total_downloaded']) ?></div>
            <div class="text-sm text-gray-400">Downloaded</div>
        </div>
    </div>
</div>

<!-- Activity Chart -->
<div class="bg-gray-800 shadow rounded-lg border border-gray-700 p-6 mb-6">
    <h2 class="text-xl font-semibold text-white mb-4">Announce Activity (24 Hours)</h2>
    <div id="activityChart" style="height: 300px;"></div>
</div>

<!-- Side-by-side containers -->
<div class="grid grid-cols-1 lg:grid-cols-2 gap-6 mb-6">
    <!-- Database Shards -->
    <div class="bg-gray-800 shadow rounded-lg border border-gray-700 p-6">
        <h2 class="text-xl font-semibold text-white mb-4">
            <i data-lucide="database" class="inline w-5 h-5"></i>
            Database Shards
        </h2>
        <div class="space-y-4">
            <?php if (empty($shardInfo)): ?>
            <p class="text-gray-400 text-center py-4">No shards found</p>
            <?php else: ?>
                <?php foreach ($shardInfo as $shard): ?>
                <div>
                    <div class="flex justify-between text-sm mb-1">
                        <span class="text-gray-300"><?= htmlspecialchars($shard['name']) ?></span>
                        <span class="text-gray-400"><?= $shard['sizeFmt'] ?></span>
                    </div>
                    <div class="w-full bg-gray-700 rounded-full h-2">
                        <div class="bg-blue-600 h-2 rounded-full" style="width: <?= min(100, $shard['percentage']) ?>%"></div>
                    </div>
                    <div class="text-xs text-gray-500 mt-1">
                        <?= number_format($shard['percentage'], 1) ?>% of 84GB limit
                    </div>
                </div>
                <?php endforeach; ?>
            <?php endif; ?>
        </div>

        <div class="mt-6 p-4 bg-gray-900 rounded-lg">
            <div class="flex items-center text-sm text-gray-300">
                <i data-lucide="info" class="w-4 h-4 mr-2 text-blue-400"></i>
                <span>SQLite with WAL mode • Auto-rotation at 84GB</span>
            </div>
        </div>
    </div>

    <!-- Top Torrents -->
    <div class="bg-gray-800 shadow rounded-lg border border-gray-700 p-6">
        <h2 class="text-xl font-semibold text-white mb-4">
            <i data-lucide="trending-up" class="inline w-5 h-5"></i>
            Top Torrents (Last Hour)
        </h2>
        <div class="space-y-3">
            <?php if (empty($topTorrents)): ?>
            <p class="text-gray-400 text-center py-4">No activity yet</p>
            <?php else: ?>
                <?php
                $maxAnnounces = max(array_column($topTorrents, 'announce_count'));
                foreach ($topTorrents as $i => $torrent):
                    $percentage = ($torrent['announce_count'] / $maxAnnounces) * 100;
                ?>
                <div>
                    <div class="flex justify-between text-sm mb-1">
                        <span class="text-blue-400">#<?= $i + 1 ?> Torrent <?= $torrent['torrent_id'] ?></span>
                        <span class="text-white font-medium"><?= number_format($torrent['announce_count']) ?> announces</span>
                    </div>
                    <div class="w-full bg-gray-700 rounded-full h-2">
                        <div class="bg-purple-600 h-2 rounded-full transition-all duration-300"
                             style="width: <?= $percentage ?>%"></div>
                    </div>
                </div>
                <?php endforeach; ?>
            <?php endif; ?>
        </div>
    </div>
</div>

<!-- System Information -->
<div class="bg-gray-800 shadow rounded-lg border border-gray-700 p-6">
    <h2 class="text-xl font-semibold text-white mb-4">
        <i data-lucide="server" class="inline w-5 h-5"></i>
        System Information
    </h2>
    <div class="grid grid-cols-1 md:grid-cols-3 gap-6">
        <div>
            <h3 class="text-sm font-medium text-gray-400 mb-2">Tracker Version</h3>
            <p class="text-white">Ocelot Go Edition v1.0</p>
            <p class="text-sm text-gray-500">SQLite + WAL Architecture</p>
        </div>
        <div>
            <h3 class="text-sm font-medium text-gray-400 mb-2">PHP Version</h3>
            <p class="text-white"><?= phpversion() ?></p>
            <p class="text-sm text-gray-500">Admin Panel Backend</p>
        </div>
        <div>
            <h3 class="text-sm font-medium text-gray-400 mb-2">Database Shards</h3>
            <p class="text-white"><?= count($shardInfo) ?> active</p>
            <p class="text-sm text-gray-500">84GB auto-rotation</p>
        </div>
    </div>
</div>

<script>
// Hourly Activity Chart
const activityData = <?= json_encode($hourlyActivity) ?>;

const margin = {top: 20, right: 30, bottom: 50, left: 60};
const width = document.getElementById('activityChart').clientWidth - margin.left - margin.right;
const height = 300 - margin.top - margin.bottom;

const svg = d3.select('#activityChart')
    .append('svg')
    .attr('width', width + margin.left + margin.right)
    .attr('height', height + margin.top + margin.bottom)
    .append('g')
    .attr('transform', `translate(${margin.left},${margin.top})`);

// Scales
const x = d3.scaleBand()
    .domain(activityData.map(d => d.hour))
    .range([0, width])
    .padding(0.1);

const y = d3.scaleLinear()
    .domain([0, d3.max(activityData, d => d.announces) || 100])
    .nice()
    .range([height, 0]);

// Axes
svg.append('g')
    .attr('transform', `translate(0,${height})`)
    .call(d3.axisBottom(x))
    .selectAll('text')
    .attr('fill', '#9ca3af')
    .attr('transform', 'rotate(-45)')
    .style('text-anchor', 'end');

svg.append('g')
    .call(d3.axisLeft(y))
    .selectAll('text')
    .attr('fill', '#9ca3af');

// Grid lines
svg.append('g')
    .attr('class', 'grid')
    .call(d3.axisLeft(y)
        .tickSize(-width)
        .tickFormat('')
    )
    .selectAll('line')
    .attr('stroke', '#374151')
    .attr('stroke-dasharray', '2,2');

// Bars
svg.selectAll('.bar')
    .data(activityData)
    .enter()
    .append('rect')
    .attr('class', 'bar')
    .attr('x', d => x(d.hour))
    .attr('y', d => y(d.announces))
    .attr('width', x.bandwidth())
    .attr('height', d => height - y(d.announces))
    .attr('fill', '#8b5cf6')
    .attr('opacity', 0.8)
    .on('mouseover', function() {
        d3.select(this).attr('opacity', 1);
    })
    .on('mouseout', function() {
        d3.select(this).attr('opacity', 0.8);
    });

// Labels on bars
svg.selectAll('.label')
    .data(activityData)
    .enter()
    .append('text')
    .attr('class', 'label')
    .attr('x', d => x(d.hour) + x.bandwidth() / 2)
    .attr('y', d => y(d.announces) - 5)
    .attr('text-anchor', 'middle')
    .attr('fill', '#e5e7eb')
    .attr('font-size', '10px')
    .text(d => d.announces > 0 ? d.announces : '');

// Y-axis label
svg.append('text')
    .attr('transform', 'rotate(-90)')
    .attr('y', 0 - margin.left)
    .attr('x', 0 - (height / 2))
    .attr('dy', '1em')
    .style('text-anchor', 'middle')
    .attr('fill', '#9ca3af')
    .text('Announce Count');
</script>

<?php include 'includes/footer.php'; ?>
