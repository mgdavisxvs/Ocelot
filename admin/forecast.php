<?php
require_once 'config.php';
requireAuth();

$pageTitle = 'Torrent Forecast';

// ── Pull torrent Markov chain from sidecar ───────────────────────────────
$chainT    = MarkovAPI::chainTorrent();   // returns null when offline
$freeleech = MarkovAPI::freeleech();      // high dead-prob candidates

$sidecarOnline = ($chainT !== null);

$torrentStates = ['THRIVING','HEALTHY','AT_RISK','DYING','DEAD'];
$stateColors   = [
    'THRIVING' => '#22c55e',
    'HEALTHY'  => '#86efac',
    'AT_RISK'  => '#facc15',
    'DYING'    => '#f97316',
    'DEAD'     => '#ef4444',
];

// Build a 5×5 transition matrix from the Markov chain response.
// chainT rows: [{from: "THRIVING", transitions: {THRIVING: 0.9, HEALTHY: 0.1, ...}}, ...]
$transMatrix = [];
for ($i = 0; $i < 5; $i++) {
    $transMatrix[$i] = array_fill(0, 5, 0.0);
    $transMatrix[$i][$i] = 1.0; // default: absorbing state (no data → stays put)
}

if ($chainT) {
    foreach ($chainT as $row) {
        $from = array_search($row['from'], $torrentStates, true);
        if ($from === false) continue;
        foreach ($row['transitions'] ?? [] as $toName => $prob) {
            $to = array_search($toName, $torrentStates, true);
            if ($to === false) continue;
            $transMatrix[$from][$to] = (float)$prob;
        }
    }
}

// ── Compute 30-day forecast for each starting state ─────────────────────
// Distribution[t] = initial × T^t   (matrix-vector multiplication, 30 steps)
$horizonDays = 30;

function matVec(array $T, array $v): array {
    $n = count($v);
    $out = array_fill(0, $n, 0.0);
    for ($i = 0; $i < $n; $i++) {
        for ($j = 0; $j < $n; $j++) {
            $out[$i] += $T[$j][$i] * $v[$j];
        }
    }
    return $out;
}

// Produce a forecast trace per starting state
$forecasts = [];
foreach ($torrentStates as $si => $startState) {
    $v = array_fill(0, 5, 0.0);
    $v[$si] = 1.0;
    $trace = [];
    for ($day = 0; $day <= $horizonDays; $day++) {
        $trace[] = $v;
        $v = matVec($transMatrix, $v);
    }
    $forecasts[$startState] = $trace;
}

// ── Active torrent counts per state from DB ──────────────────────────────
$activeCounts = [];
try {
    $db = OcelotDB::connect();
    $rows = $db->query("
        SELECT
            CASE
                WHEN seeders = 0 AND leechers = 0 THEN 'DEAD'
                WHEN seeders = 0 THEN 'DYING'
                WHEN seeders < 3 THEN 'AT_RISK'
                WHEN seeders < 10 THEN 'HEALTHY'
                ELSE 'THRIVING'
            END AS state,
            COUNT(*) AS cnt
        FROM torrents
        GROUP BY 1
    ")->fetchAll();
    foreach ($rows as $r) {
        $activeCounts[$r['state']] = (int)$r['cnt'];
    }
} catch (Exception $e) {
    $activeCounts = [];
}

// Blended forecast: weight each starting-state forecast by actual torrent count
$totalTorrents = array_sum($activeCounts);
$blendedTrace  = [];
for ($day = 0; $day <= $horizonDays; $day++) {
    $blended = array_fill(0, 5, 0.0);
    foreach ($torrentStates as $si => $state) {
        $weight = $totalTorrents > 0 ? (($activeCounts[$state] ?? 0) / $totalTorrents) : (1 / 5);
        foreach ($forecasts[$state][$day] as $j => $prob) {
            $blended[$j] += $weight * $prob;
        }
    }
    $blendedTrace[] = $blended;
}

$forecastJson     = json_encode($forecasts);
$blendedJson      = json_encode($blendedTrace);
$stateColorsJson  = json_encode($stateColors);
$torrentStatesJson = json_encode($torrentStates);
$activeCountsJson  = json_encode($activeCounts);
$freeleechJson     = json_encode($freeleech ?? []);

include 'includes/header.php';
?>

<div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-6">
    <!-- Header -->
    <div class="mb-6 flex items-center justify-between">
        <div>
            <h1 class="text-2xl font-bold text-white">Torrent Health Forecast</h1>
            <p class="mt-1 text-sm text-gray-400">30-day state distribution forecast using Markov chain simulation</p>
        </div>
        <span class="inline-flex items-center gap-1.5 text-xs <?= $sidecarOnline ? 'text-green-400' : 'text-yellow-400' ?>">
            <span class="h-2 w-2 rounded-full <?= $sidecarOnline ? 'bg-green-400' : 'bg-yellow-400' ?>"></span>
            <?= $sidecarOnline ? 'Sidecar online' : 'Using identity matrix (sidecar offline)' ?>
        </span>
    </div>

    <!-- Current state tiles -->
    <div class="grid grid-cols-2 sm:grid-cols-5 gap-3 mb-6">
        <?php foreach ($torrentStates as $state):
            $cnt = $activeCounts[$state] ?? 0;
            $pct = $totalTorrents > 0 ? round(100 * $cnt / $totalTorrents) : 0;
        ?>
        <div class="rounded-lg bg-gray-800 border border-gray-700 p-3">
            <div class="text-xs text-gray-400 mb-1"><?= $state ?></div>
            <div class="text-xl font-mono font-semibold" style="color:<?= $stateColors[$state] ?>"><?= $cnt ?></div>
            <div class="mt-1 h-1 rounded-full bg-gray-700 overflow-hidden">
                <div class="h-1 rounded-full" style="width:<?= $pct ?>%;background:<?= $stateColors[$state] ?>"></div>
            </div>
        </div>
        <?php endforeach; ?>
    </div>

    <!-- Blended forecast area chart -->
    <div class="rounded-lg bg-gray-800 border border-gray-700 p-4 mb-6">
        <h2 class="text-sm font-semibold text-gray-300 mb-3">Blended Fleet Forecast — 30 Days</h2>
        <div id="forecast-chart" style="width:100%;height:300px;"></div>
    </div>

    <!-- Per-state forecast lines -->
    <div class="rounded-lg bg-gray-800 border border-gray-700 p-4 mb-6">
        <h2 class="text-sm font-semibold text-gray-300 mb-1">Survival Probability by Starting State</h2>
        <p class="text-xs text-gray-500 mb-3">Probability of reaching DEAD over 30 days from each starting state</p>
        <div id="survival-chart" style="width:100%;height:260px;"></div>
    </div>

    <!-- Freeleech candidates -->
    <?php if (!empty($freeleech)): ?>
    <div class="rounded-lg bg-gray-800 border border-gray-700 overflow-hidden">
        <div class="px-4 py-3 border-b border-gray-700">
            <h2 class="text-sm font-semibold text-gray-300">High-Risk Torrents (Freeleech Candidates)</h2>
        </div>
        <table class="min-w-full divide-y divide-gray-700 text-sm">
            <thead>
                <tr>
                    <th class="px-4 py-2 text-left text-xs font-medium text-gray-400 uppercase">Torrent ID</th>
                    <th class="px-4 py-2 text-right text-xs font-medium text-gray-400 uppercase">Priority Score</th>
                    <th class="px-4 py-2 text-right text-xs font-medium text-gray-400 uppercase">72h Dead Prob</th>
                </tr>
            </thead>
            <tbody class="divide-y divide-gray-700">
                <?php foreach (array_slice($freeleech, 0, 20) as $cand): ?>
                <tr class="hover:bg-gray-700/40">
                    <td class="px-4 py-2 font-mono text-gray-200"><?= (int)($cand['torrent_id'] ?? 0) ?></td>
                    <td class="px-4 py-2 text-right text-blue-300 font-mono"><?= number_format((float)($cand['priority_score'] ?? 0), 4) ?></td>
                    <td class="px-4 py-2 text-right font-mono <?= ($cand['dead_prob_72h'] ?? 0) > 0.5 ? 'text-red-400' : 'text-yellow-400' ?>">
                        <?= number_format((float)($cand['dead_prob_72h'] ?? 0) * 100, 1) ?>%
                    </td>
                </tr>
                <?php endforeach; ?>
            </tbody>
        </table>
    </div>
    <?php endif; ?>
</div>

<script>
(function () {
    const blended    = <?= $blendedJson ?>;
    const forecasts  = <?= $forecastJson ?>;
    const colors     = <?= $stateColorsJson ?>;
    const states     = <?= $torrentStatesJson ?>;

    const days = blended.map((_, i) => i);

    // ── Stacked area chart (blended fleet) ───────────────────────────────
    (function () {
        const el = document.getElementById('forecast-chart');
        if (!el) return;
        const W = el.clientWidth || 720, H = 300;
        const m = { top: 10, right: 20, bottom: 30, left: 44 };

        const svg = d3.select('#forecast-chart').append('svg')
            .attr('width', W).attr('height', H)
            .attr('viewBox', `0 0 ${W} ${H}`)
            .style('width', '100%');

        const x = d3.scaleLinear().domain([0, blended.length - 1]).range([m.left, W - m.right]);
        const y = d3.scaleLinear().domain([0, 1]).range([H - m.bottom, m.top]);

        // Build stacked series
        const series = states.map((s, si) => ({
            name: s,
            values: blended.map(row => row[si]),
        }));

        // Compute cumulative sums for stacking
        const stacked = series.map((s, si) => ({
            name: s.name,
            lower: blended.map((row, di) => states.slice(0, si).reduce((acc, _, j) => acc + blended[di][j], 0)),
            upper: blended.map((row, di) => states.slice(0, si + 1).reduce((acc, _, j) => acc + blended[di][j], 0)),
        }));

        const area = d3.area()
            .x((_, i) => x(i))
            .y0(d => y(d.lo))
            .y1(d => y(d.hi));

        stacked.forEach(band => {
            const data = band.lower.map((lo, i) => ({ lo, hi: band.upper[i] }));
            svg.append('path')
                .datum(data)
                .attr('d', area)
                .attr('fill', colors[band.name])
                .attr('opacity', 0.75);
        });

        // Axes
        svg.append('g').attr('transform', `translate(0,${H - m.bottom})`)
            .call(d3.axisBottom(x).ticks(10).tickFormat(d => `d${d}`))
            .selectAll('text').style('fill', '#9ca3af').style('font-size', '10px');
        svg.append('g').attr('transform', `translate(${m.left},0)`)
            .call(d3.axisLeft(y).tickFormat(d => `${(d * 100).toFixed(0)}%`).ticks(5))
            .selectAll('text').style('fill', '#9ca3af').style('font-size', '10px');

        // Legend
        states.forEach((s, i) => {
            svg.append('rect').attr('x', W - m.right - 110).attr('y', m.top + i * 18)
                .attr('width', 10).attr('height', 10).attr('rx', 2).attr('fill', colors[s]);
            svg.append('text').attr('x', W - m.right - 96).attr('y', m.top + i * 18 + 9)
                .style('fill', '#d1d5db').style('font-size', '10px').text(s);
        });
    })();

    // ── Survival probability lines ─────────────────────────────────────
    (function () {
        const el = document.getElementById('survival-chart');
        if (!el) return;
        const W = el.clientWidth || 720, H = 260;
        const m = { top: 10, right: 130, bottom: 30, left: 44 };
        const deadIdx = states.indexOf('DEAD');

        const svg = d3.select('#survival-chart').append('svg')
            .attr('width', W).attr('height', H)
            .attr('viewBox', `0 0 ${W} ${H}`)
            .style('width', '100%');

        const x = d3.scaleLinear().domain([0, 30]).range([m.left, W - m.right]);
        const y = d3.scaleLinear().domain([0, 1]).range([H - m.bottom, m.top]);

        const line = d3.line()
            .x((_, i) => x(i))
            .y(d => y(d));

        states.forEach(state => {
            const deadProbs = (forecasts[state] || []).map(dist => dist[deadIdx] || 0);
            svg.append('path')
                .datum(deadProbs)
                .attr('fill', 'none')
                .attr('stroke', colors[state])
                .attr('stroke-width', 2)
                .attr('d', line);

            // End-label
            const last = deadProbs[deadProbs.length - 1];
            svg.append('text')
                .attr('x', W - m.right + 4)
                .attr('y', y(last) + 4)
                .style('fill', colors[state])
                .style('font-size', '10px')
                .text(`${state} ${(last * 100).toFixed(1)}%`);
        });

        svg.append('g').attr('transform', `translate(0,${H - m.bottom})`)
            .call(d3.axisBottom(x).ticks(10).tickFormat(d => `d${d}`))
            .selectAll('text').style('fill', '#9ca3af').style('font-size', '10px');
        svg.append('g').attr('transform', `translate(${m.left},0)`)
            .call(d3.axisLeft(y).tickFormat(d => `${(d * 100).toFixed(0)}%`).ticks(5))
            .selectAll('text').style('fill', '#9ca3af').style('font-size', '10px');
    })();
})();
</script>

<?php include 'includes/footer.php'; ?>
