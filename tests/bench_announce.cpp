// Tier 7 — BM-01 through BM-05
// Standalone benchmark (no test framework dep).
// Build: cmake -DBUILD_TESTS=ON -DCMAKE_BUILD_TYPE=Release ..
// Run:   ./tests/bench_announce
#include <iostream>
#include <chrono>
#include <string>
#include <memory>
#include <iomanip>
#include "test_helpers.h"

using Clock = std::chrono::high_resolution_clock;
using Dur   = std::chrono::duration<double>;

static void print_result(const char *name, long ops, double elapsed_s) {
    double rate = ops / elapsed_s;
    std::cout << std::left << std::setw(40) << name
              << std::right << std::setw(10) << std::fixed << std::setprecision(0)
              << rate << " ops/s"
              << "  (" << std::setprecision(3) << elapsed_s << " s for "
              << ops << " ops)\n";
}

// BM-01: single-threaded announce throughput
static void bm_announce_throughput(WorkerFixture &fix) {
    const long N = 50000;
    std::string query = make_announce_query(
        percent_encode(TEST_INFO_HASH), percent_encode(TEST_PEER_ID),
        TEST_PORT, 0, 0, 1000, "started");
    std::string req = make_get_request(TEST_PASSKEY, "announce", query);

    auto t0 = Clock::now();
    for (long i = 0; i < N; i++) {
        std::string ip = TEST_PEER_IP;
        client_opts_t opts = {false, false, false};
        fix.w->work(req, ip, opts);
    }
    auto t1 = Clock::now();
    print_result("BM-01 announce (single thread)", N, Dur(t1 - t0).count());
}

// BM-03: peer list construction with pre-populated swarm (100 peers)
static void bm_peer_list_construction(WorkerFixture &fix) {
    // Populate 100 seeders
    for (int i = 0; i < 100; i++) {
        std::string peer_id(20, (char)(0x10 + (i & 0xFF)));
        std::string passkey = "seeder" + std::string(26 - std::to_string(i).size(), '0') + std::to_string(i);
        passkey.resize(32, '0');
        auto u = std::make_shared<user>((userid_t)(100 + i), true, false);
        fix.users[passkey] = u;

        std::string query = make_announce_query(
            percent_encode(TEST_INFO_HASH), percent_encode(peer_id),
            6000 + i, 1000LL*i, 0, 0, "started", 50, "10.0." + std::to_string(i/256) + "." + std::to_string(i%256));
        std::string req = make_get_request(passkey, "announce", query);
        std::string ip = "10.0." + std::to_string(i/256) + "." + std::to_string(i%256);
        client_opts_t opts = {false, false, false};
        fix.w->work(req, ip, opts);
    }

    // Now benchmark the leecher announce (triggers peer list construction)
    const long N = 10000;
    std::string query = make_announce_query(
        percent_encode(TEST_INFO_HASH), percent_encode(TEST_PEER_ID),
        TEST_PORT, 0, 0, 1000, "started");
    std::string req = make_get_request(TEST_PASSKEY, "announce", query);

    auto t0 = Clock::now();
    for (long i = 0; i < N; i++) {
        std::string ip = TEST_PEER_IP;
        client_opts_t opts = {false, false, false};
        fix.w->work(req, ip, opts);
    }
    auto t1 = Clock::now();
    print_result("BM-03 announce (100-peer swarm)", N, Dur(t1 - t0).count());
}

// BM-05: hex_decode throughput (hot path on every announce)
static void bm_hex_decode() {
    const long N = 1000000;
    std::string encoded = percent_encode(TEST_INFO_HASH); // 60-char percent-encoded

    auto t0 = Clock::now();
    for (long i = 0; i < N; i++) {
        volatile std::string r = hex_decode(encoded);
        (void)r;
    }
    auto t1 = Clock::now();
    print_result("BM-05 hex_decode 20-byte hash", N, Dur(t1 - t0).count());
}

// BM-06: memory footprint estimate (1000 torrents × 10 peers)
static void bm_memory_footprint() {
    torrent_list torrents;
    user_list users;
    std::vector<std::string> whitelist;
    config conf;
    MockDB db;
    MockSiteComm sc;
    conf.set("site_password",   SITE_PASS);
    conf.set("report_password", REPORT_PASS);

    auto u = std::make_shared<user>(1u, true, false);
    users[TEST_PASSKEY] = u;

    const int TORRENTS = 1000;
    const int PEERS_PER = 10;

    // Insert torrents
    for (int t = 0; t < TORRENTS; t++) {
        std::string hash(20, (char)(t & 0xFF));
        hash[0] = (char)((t >> 8) & 0xFF);
        torrent tor;
        tor.id = (torid_t)t;
        tor.balance = 0;
        tor.completed = 0;
        tor.free_torrent = NORMAL;
        tor.last_selected_seeder = "";
        torrents[hash] = tor;
    }

    worker w(&conf, torrents, users, whitelist, &db, &sc);

    auto t0 = Clock::now();
    int total_peers = 0;
    for (int t = 0; t < TORRENTS; t++) {
        std::string hash(20, (char)(t & 0xFF));
        hash[0] = (char)((t >> 8) & 0xFF);
        for (int p = 0; p < PEERS_PER; p++) {
            std::string peer_id(20, (char)(p + 1));
            std::string passkey(32, (char)('a' + (p % 26)));
            passkey[0] = (char)('0' + (t % 10));
            if (users.find(passkey) == users.end()) {
                users[passkey] = std::make_shared<user>((userid_t)(t * 1000 + p), true, false);
            }
            std::string query = make_announce_query(
                percent_encode(hash), percent_encode(peer_id),
                6000 + p, 0, 0, 1000);
            std::string req = make_get_request(passkey, "announce", query);
            std::string ip = "10.0.0.1";
            client_opts_t opts = {false, false, false};
            w.work(req, ip, opts);
            total_peers++;
        }
    }
    auto t1 = Clock::now();

    std::cout << "BM-06 swarm insert: "
              << TORRENTS << " torrents × " << PEERS_PER << " peers = "
              << total_peers << " total peers in "
              << std::fixed << std::setprecision(3) << Dur(t1 - t0).count() << " s\n";
}

int main() {
    std::cout << "\n=== Ocelot Benchmarks ===\n\n";

    {
        WorkerFixture fix;
        bm_announce_throughput(fix);
    }
    {
        WorkerFixture fix;
        bm_peer_list_construction(fix);
    }
    bm_hex_decode();
    bm_memory_footprint();

    std::cout << "\nDone.\n";
    return 0;
}
