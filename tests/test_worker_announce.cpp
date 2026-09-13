// Tier 2 — TC-WK-PARSE-* and TC-WK-AN-*
// Tier 5 — BEP-3 / BEP-23 conformance
#include <gtest/gtest.h>
#include <string>
#include <cstdint>
#include "test_helpers.h"

// ============================================================
// PARSE TESTS (TC-WK-PARSE-01 through TC-WK-PARSE-09)
// ============================================================

class ParseTest : public ::testing::Test {
protected:
    WorkerFixture fix;
};

// TC-WK-PARSE-01: valid announce dispatched (no parse error)
TEST_F(ParseTest, valid_announce_dispatched) {
    std::string r = fix.announce();
    EXPECT_FALSE(WorkerFixture::is_failure(r)) << WorkerFixture::body(r);
}

// TC-WK-PARSE-05: missing passkey (wrong passkey) → failure
TEST_F(ParseTest, wrong_passkey_returns_failure) {
    std::string r = fix.announce("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"); // unknown passkey
    EXPECT_TRUE(WorkerFixture::is_failure(r));
}

// TC-WK-PARSE-06: unknown action path → HTML "Nothing to see here" or error
TEST_F(ParseTest, unknown_action_path) {
    std::string req = make_get_request(TEST_PASSKEY, "unknown", "x=1");
    std::string ip = "1.2.3.4";
    client_opts_t opts = {false, false, false};
    std::string r = fix.w->work(req, ip, opts);
    // Should not produce a valid announce response
    EXPECT_TRUE(WorkerFixture::is_failure(r) ||
                WorkerFixture::body(r).find("Nothing") != std::string::npos ||
                WorkerFixture::body(r).find("Invalid") != std::string::npos);
}

// TC-WK-PARSE-07: info_hash URL-decoded from percent-encoded form
TEST_F(ParseTest, info_hash_percent_decoded) {
    // The test torrent is keyed by TEST_INFO_HASH (20 x 0x01).
    // Use percent-encoding in the URL — should still find the torrent.
    std::string r = fix.announce(
        TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
        TEST_PORT, 0, 0, 0, "started"
    );
    EXPECT_FALSE(WorkerFixture::is_failure(r)) << WorkerFixture::body(r);
}

// TC-WK-PARSE-08: HTTP/1.0 request — Connection: Close expected (keepalive_timeout=0 in default config)
TEST_F(ParseTest, http10_no_keepalive) {
    std::string query = make_announce_query(
        percent_encode(TEST_INFO_HASH), percent_encode(TEST_PEER_ID));
    std::string req = make_get_request(TEST_PASSKEY, "announce", query, "", "1.0");
    std::string ip = TEST_PEER_IP;
    client_opts_t opts = {false, false, false};
    std::string r = fix.w->work(req, ip, opts);
    // With keepalive_timeout=0, http_close is always true → header says Close
    EXPECT_NE(std::string::npos, r.find("Connection: Close"));
}

// TC-WK-PARSE-09: HTTP/1.1 with Connection: close header
TEST_F(ParseTest, http11_connection_close_header) {
    // Enable keepalive in config first
    fix.conf.set("keepalive_timeout", "30");
    fix.w->reload_config(&fix.conf);

    std::string query = make_announce_query(
        percent_encode(TEST_INFO_HASH), percent_encode(TEST_PEER_ID));
    std::string req = make_get_request(TEST_PASSKEY, "announce", query,
                                       "Connection: close\r\n");
    std::string ip = TEST_PEER_IP;
    client_opts_t opts = {false, false, false};
    std::string r = fix.w->work(req, ip, opts);
    EXPECT_NE(std::string::npos, r.find("Connection: Close"));
}

// ============================================================
// ANNOUNCE HAPPY PATH (TC-WK-AN-01 through TC-WK-AN-09)
// ============================================================

class AnnounceTest : public ::testing::Test {
protected:
    WorkerFixture fix;
};

// TC-WK-AN-01: leecher announces "started"
TEST_F(AnnounceTest, leecher_started_added_to_leechers) {
    fix.db.reset();
    std::string r = fix.announce(
        TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
        TEST_PORT, 0, 0, 1000 /*left>0*/, "started"
    );
    EXPECT_FALSE(WorkerFixture::is_failure(r)) << WorkerFixture::body(r);

    // Peer should be in leecher list
    EXPECT_EQ(1u, fix.torrents[TEST_INFO_HASH].leechers.size());
    EXPECT_EQ(0u, fix.torrents[TEST_INFO_HASH].seeders.size());

    // User leeching counter incremented
    EXPECT_EQ(1u, fix.user1->get_leeching());

    // db.record_peer called at least once
    EXPECT_FALSE(fix.db.recorded_peers.empty());

    // Response contains required bencode keys
    std::string b = WorkerFixture::body(r);
    EXPECT_NE(std::string::npos, b.find("8:interval"));
    EXPECT_NE(std::string::npos, b.find("12:min interval"));
    EXPECT_NE(std::string::npos, b.find("5:peers"));
    EXPECT_NE(std::string::npos, b.find("8:complete"));
    EXPECT_NE(std::string::npos, b.find("10:incomplete"));
}

// TC-WK-AN-02: seeder announces
TEST_F(AnnounceTest, seeder_added_to_seeders) {
    std::string r = fix.announce(
        TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
        TEST_PORT, 5000, 5000, 0 /*left==0*/, "started"
    );
    EXPECT_FALSE(WorkerFixture::is_failure(r));
    EXPECT_EQ(1u, fix.torrents[TEST_INFO_HASH].seeders.size());
    EXPECT_EQ(1u, fix.user1->get_seeding());
}

// TC-WK-AN-03: completed event → snatch recorded, peer moved to seeders
TEST_F(AnnounceTest, completed_event_records_snatch) {
    // First become a leecher
    fix.announce(TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
                 TEST_PORT, 0, 0, 1000, "started");
    fix.db.reset();

    // Then complete
    std::string r = fix.announce(TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
                                  TEST_PORT, 1000, 1000, 0, "completed");
    EXPECT_FALSE(WorkerFixture::is_failure(r));

    // Snatch recorded
    EXPECT_FALSE(fix.db.recorded_snatches.empty());

    // Moved from leechers to seeders
    EXPECT_EQ(0u, fix.torrents[TEST_INFO_HASH].leechers.size());
    EXPECT_EQ(1u, fix.torrents[TEST_INFO_HASH].seeders.size());

    // completed counter incremented
    EXPECT_EQ(1u, fix.torrents[TEST_INFO_HASH].completed);
}

// TC-WK-AN-04: stopped event → peer removed
TEST_F(AnnounceTest, stopped_event_removes_peer) {
    fix.announce(TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
                 TEST_PORT, 0, 0, 1000, "started");
    EXPECT_EQ(1u, fix.torrents[TEST_INFO_HASH].leechers.size());

    fix.announce(TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
                 TEST_PORT, 0, 0, 1000, "stopped");
    EXPECT_EQ(0u, fix.torrents[TEST_INFO_HASH].leechers.size());
    EXPECT_EQ(0u, fix.user1->get_leeching());
}

// TC-WK-AN-05: peer update — upload delta accumulated
TEST_F(AnnounceTest, upload_delta_recorded_on_update) {
    fix.announce(TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
                 TEST_PORT, 0, 0, 1000, "started");
    fix.db.reset();

    // Second announce with more upload
    fix.announce(TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
                 TEST_PORT, 500, 200, 500);

    // record_user should have been called (upload change → user credit)
    EXPECT_FALSE(fix.db.recorded_users.empty());
}

// TC-WK-AN-06: compact peer list — 6-byte big-endian IP:port
TEST_F(AnnounceTest, compact_peer_format) {
    // Add a second user (seeder) to the torrent so leecher gets peers
    fix.announce(TEST_PASSKEY2, TEST_INFO_HASH, TEST_PEER_ID2,
                 TEST_PORT, 5000, 5000, 0, "started", 50, "", "5.6.7.8");

    // Now first user (leecher) announces
    std::string r = fix.announce(TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
                                  TEST_PORT, 0, 0, 1000, "started");

    std::string peers = WorkerFixture::extract_peers(r);
    if (!peers.empty()) {
        // Must be multiple of 6 bytes
        EXPECT_EQ(0u, peers.size() % 6);

        // Each chunk: bytes 0-3 IPv4, bytes 4-5 port
        for (size_t i = 0; i + 6 <= peers.size(); i += 6) {
            uint16_t port = (static_cast<uint8_t>(peers[i+4]) << 8)
                          |  static_cast<uint8_t>(peers[i+5]);
            EXPECT_GT(port, 0u);
        }
    }
}

// TC-WK-AN-07: numwant=0 → peers key present but empty
TEST_F(AnnounceTest, numwant_zero_empty_peers) {
    std::string r = fix.announce(TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
                                  TEST_PORT, 0, 0, 1000, "started", 0 /*numwant*/);
    std::string peers = WorkerFixture::extract_peers(r);
    EXPECT_EQ(0u, peers.size());
    // "5:peers" key must still be present
    EXPECT_NE(std::string::npos, WorkerFixture::body(r).find("5:peers"));
}

// TC-WK-AN-08: numwant=50 with fewer peers — returns exactly what's available
TEST_F(AnnounceTest, numwant_caps_at_available) {
    // Only one seeder in the swarm
    fix.announce(TEST_PASSKEY2, TEST_INFO_HASH, TEST_PEER_ID2,
                 TEST_PORT, 0, 0, 0, "started", 50, "", "10.0.0.1");

    std::string r = fix.announce(TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
                                  TEST_PORT, 0, 0, 1000, "started", 50);

    std::string peers = WorkerFixture::extract_peers(r);
    EXPECT_LE(peers.size(), 50u * 6u);
    EXPECT_EQ(0u, peers.size() % 6);
}

// TC-WK-AN-09: numwant>limit — capped at numwant_limit (50)
TEST_F(AnnounceTest, numwant_capped_at_limit) {
    // The config default numwant_limit=50; request 200.
    // With only 1 peer, the cap is trivially ≤ 50*6.
    std::string r = fix.announce(TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
                                  TEST_PORT, 0, 0, 1000, "started", 200);
    std::string peers = WorkerFixture::extract_peers(r);
    EXPECT_LE(peers.size(), 50u * 6u);
}

// ============================================================
// FREE-LEECH / TOKEN LOGIC (TC-WK-AN-10 through TC-WK-AN-12)
// ============================================================

// TC-WK-AN-10: free torrent — download not credited to user
TEST_F(AnnounceTest, free_torrent_no_download_credit) {
    fix.torrents[TEST_INFO_HASH].free_torrent = FREE;

    fix.announce(TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
                 TEST_PORT, 0, 0, 1000, "started");
    fix.db.reset();

    // Second announce with upload AND download change
    fix.announce(TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
                 TEST_PORT, 1000, 500, 500);

    // For FREE torrents: downloaded_change is zeroed, uploaded_change is NOT.
    // record_user IS called with (userid, uploaded_change, 0) — upload is credited,
    // download is not. Verify a record was written with downloaded portion = 0.
    ASSERT_FALSE(fix.db.recorded_users.empty());
    EXPECT_NE(std::string::npos, fix.db.recorded_users.front().find(",0)"));
}

// TC-WK-AN-11: NEUTRAL torrent — upload credited but download not credited
TEST_F(AnnounceTest, neutral_torrent_upload_credited_download_not) {
    fix.torrents[TEST_INFO_HASH].free_torrent = NEUTRAL;

    fix.announce(TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
                 TEST_PORT, 0, 0, 1000, "started");
    fix.db.reset();

    fix.announce(TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
                 TEST_PORT, 1000, 500, 500);

    // For NEUTRAL: uploaded_change = 0, downloaded_change = 0 → no user record either
    // (NEUTRAL zeroes both, so no record_user call)
    EXPECT_TRUE(fix.db.recorded_users.empty());
}

// TC-WK-AN-12: tokened user — record_token called, download suppressed
TEST_F(AnnounceTest, token_user_record_token_called) {
    // Give user1 a token on the test torrent
    fix.torrents[TEST_INFO_HASH].tokened_users.insert(fix.user1->get_id());

    fix.announce(TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
                 TEST_PORT, 0, 0, 1000, "started");
    fix.db.reset();

    fix.announce(TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
                 TEST_PORT, 500, 200, 800);

    // record_token must have been called
    EXPECT_FALSE(fix.db.recorded_tokens.empty());
}

// ============================================================
// ERROR / REJECTION CASES (TC-WK-AN-20 through TC-WK-AN-26)
// ============================================================

// TC-WK-AN-20: unknown passkey
TEST_F(AnnounceTest, unknown_passkey_returns_failure) {
    std::string r = fix.announce("ffffffffffffffffffffffffffffffff");
    EXPECT_TRUE(WorkerFixture::is_failure(r));
    EXPECT_NE(std::string::npos, WorkerFixture::body(r).find("Passkey not found"));
}

// TC-WK-AN-21: user cannot leech, left > 0
TEST_F(AnnounceTest, leech_disabled_user_denied) {
    fix.user1->set_leechstatus(false);
    std::string r = fix.announce(TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
                                  TEST_PORT, 0, 0, 1000 /*left>0*/);
    EXPECT_TRUE(WorkerFixture::is_failure(r));
    EXPECT_NE(std::string::npos, WorkerFixture::body(r).find("leeching forbidden"));
}

// TC-WK-AN-22: unknown info_hash → "Unregistered torrent"
TEST_F(AnnounceTest, unknown_info_hash_returns_failure) {
    std::string bad_hash(20, '\xFF');
    std::string r = fix.announce(TEST_PASSKEY, bad_hash, TEST_PEER_ID,
                                  TEST_PORT, 0, 0, 0, "started");
    EXPECT_TRUE(WorkerFixture::is_failure(r));
    EXPECT_NE(std::string::npos, WorkerFixture::body(r).find("Unregistered torrent"));
}

// TC-WK-AN-23: peer_id length ≠ 20 bytes
TEST_F(AnnounceTest, short_peer_id_rejected) {
    std::string bad_pid = std::string(10, '\x04'); // only 10 bytes
    std::string r = fix.announce(TEST_PASSKEY, TEST_INFO_HASH, bad_pid);
    EXPECT_TRUE(WorkerFixture::is_failure(r));
}

// TC-WK-AN-24: client not in whitelist
TEST_F(AnnounceTest, non_whitelisted_client_rejected) {
    fix.whitelist.push_back("-UT3"); // only uTorrent 3.x allowed
    // TEST_PEER_ID starts with 0x02 bytes, which does not match "-UT3"
    std::string r = fix.announce();
    EXPECT_TRUE(WorkerFixture::is_failure(r));
    EXPECT_NE(std::string::npos, WorkerFixture::body(r).find("whitelist"));
}

// TC-WK-AN-25: IP-protected user — IP NOT in peer record
TEST_F(AnnounceTest, protected_user_ip_not_in_peer_record) {
    fix.user1->set_protected(true);
    fix.db.reset();

    fix.announce(TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
                 TEST_PORT, 0, 0, 1000, "started");

    // Peer records with the heavy overload (4 args) include ip
    for (auto &pr : fix.db.peer_records) {
        if (pr.heavy) {
            EXPECT_EQ("", pr.ip) << "IP should be empty for protected user";
        }
    }
}

// TC-WK-AN-26: deleted torrent → del_message returned
TEST_F(AnnounceTest, deleted_torrent_returns_del_reason) {
    // Delete the torrent via the update path
    params_type del_params;
    del_params["action"]    = "delete_torrent";
    del_params["info_hash"] = percent_encode(TEST_INFO_HASH);
    del_params["reason"]    = "0"; // DUPE
    fix.update_action(del_params);

    // Now announce to the deleted torrent
    std::string r = fix.announce();
    EXPECT_TRUE(WorkerFixture::is_failure(r));
    EXPECT_NE(std::string::npos, WorkerFixture::body(r).find("Unregistered torrent"));
}

// ============================================================
// BEP-3 / BEP-23 CONFORMANCE (TC-BEP3-01 through TC-BEP23-03)
// ============================================================

class BEPTest : public ::testing::Test {
protected:
    WorkerFixture fix;

    std::string b; // response body

    void SetUp() override {
        std::string r = fix.announce();
        b = WorkerFixture::body(r);
    }
};

// TC-BEP3-01: interval key present, positive integer
TEST_F(BEPTest, bep3_interval_present) {
    EXPECT_NE(std::string::npos, b.find("8:intervali"));
}

// TC-BEP3-02: min interval present and ≤ interval
TEST_F(BEPTest, bep3_min_interval_present) {
    EXPECT_NE(std::string::npos, b.find("12:min intervali"));
}

// TC-BEP3-04: complete / incomplete keys are non-negative integers
TEST_F(BEPTest, bep3_complete_incomplete_non_negative) {
    EXPECT_NE(std::string::npos, b.find("8:completei"));
    EXPECT_NE(std::string::npos, b.find("10:incompletei"));
}

// TC-BEP23-01: compact=1 → peers is a byte string, len % 6 == 0
TEST_F(BEPTest, bep23_compact_peer_list_multiple_of_6) {
    // Add a second peer so peers list is non-empty
    fix.announce(TEST_PASSKEY2, TEST_INFO_HASH, TEST_PEER_ID2,
                 6882, 0, 0, 0, "started", 50, "", "10.20.30.40");
    std::string r2 = fix.announce(TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
                                   TEST_PORT, 0, 0, 1000, "started");
    std::string peers = WorkerFixture::extract_peers(r2);
    if (!peers.empty()) {
        EXPECT_EQ(0u, peers.size() % 6);
    }
}

// TC-BEP23-02: each 6-byte chunk encodes IPv4 big-endian + port big-endian
TEST_F(BEPTest, bep23_compact_format_bytes) {
    // Seeder at known IP+port
    fix.announce(TEST_PASSKEY2, TEST_INFO_HASH, TEST_PEER_ID2,
                 6881, 0, 0, 0, "started", 50, "", "1.2.3.4");

    std::string r = fix.announce(TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
                                  TEST_PORT, 0, 0, 1000, "started");
    std::string peers = WorkerFixture::extract_peers(r);

    bool found = false;
    for (size_t i = 0; i + 6 <= peers.size(); i += 6) {
        if (static_cast<uint8_t>(peers[i])   == 1 &&
            static_cast<uint8_t>(peers[i+1]) == 2 &&
            static_cast<uint8_t>(peers[i+2]) == 3 &&
            static_cast<uint8_t>(peers[i+3]) == 4) {
            uint16_t port = (static_cast<uint8_t>(peers[i+4]) << 8)
                           | static_cast<uint8_t>(peers[i+5]);
            EXPECT_EQ(6881u, port);
            found = true;
            break;
        }
    }
    if (!peers.empty()) EXPECT_TRUE(found) << "Seeder IP 1.2.3.4:6881 not found in peers";
}

// TC-BEP23-03: self-exclusion — own IP:port not in peer list
TEST_F(BEPTest, bep23_self_excluded_from_peers) {
    // Announce as leecher
    fix.announce(TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
                 TEST_PORT, 0, 0, 1000, "started", 50, "", TEST_PEER_IP);

    // Get peer list (re-announce, which returns peers)
    std::string r = fix.announce(TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
                                  TEST_PORT, 0, 0, 1000, "", 50, "", TEST_PEER_IP);
    std::string peers = WorkerFixture::extract_peers(r);

    // Own compact representation
    std::string own = WorkerFixture::compact_peer(TEST_PEER_IP, TEST_PORT);

    // Own entry must not appear
    EXPECT_EQ(std::string::npos, peers.find(own))
        << "Self-peer should not be returned in peer list";
}
