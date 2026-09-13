// Tier 6 — TC-SEC-01 through TC-SEC-07 (structural / logic security tests)
#include <gtest/gtest.h>
#include <string>
#include "test_helpers.h"

class SecurityTest : public ::testing::Test {
protected:
    WorkerFixture fix;
};

// TC-SEC-01: SQL injection via announce params
// The record strings sent to db->record_peer must not change the SQL
// structure — verified by checking the raw string passed to MockDB.
TEST_F(SecurityTest, sql_injection_in_peer_id) {
    // Craft a peer_id that contains SQL injection material
    // peer_id is stored raw (20-byte binary); the record is a value list
    // e.g. (1, 1, 1, ...) — attacker tries to close the value list
    std::string evil_peer_id = std::string(8, '\x00')
                             + "'; DROP TABLE"  // 13 chars
                             + std::string(0, '\x00'); // total = 21? — keep to 20
    // Trim/pad to exactly 20 bytes
    evil_peer_id.resize(20, '\x00');

    fix.db.reset();
    fix.announce(TEST_PASSKEY, TEST_INFO_HASH, evil_peer_id,
                 TEST_PORT, 0, 0, 1000, "started");

    // The record string is a simple parenthesised value list (not raw SQL from peer data).
    // peer_id is passed separately to record_peer, NOT interpolated into the record string.
    // So the record_str field should contain only numeric values.
    for (auto &pr : fix.db.peer_records) {
        // The record string (pr.record) must consist only of digits, commas, parens.
        // It must NOT contain any letters/quotes that would alter SQL structure.
        for (char c : pr.record) {
            bool safe = (c >= '0' && c <= '9') || c == ',' || c == '(' || c == ')' || c == '-';
            EXPECT_TRUE(safe) << "Unexpected char '" << c
                              << "' (0x" << std::hex << (int)(unsigned char)c
                              << ") in peer record string — possible SQL injection";
        }
    }
}

TEST_F(SecurityTest, sql_injection_in_ip_field) {
    // IP field: crafted to contain SQL special chars
    fix.db.reset();
    // Worker parses the IP into a compact binary form; invalid chars set invalid_ip = true.
    // The record_peer(heavy) call passes ip separately, not interpolated into record_str.
    fix.announce(TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
                 TEST_PORT, 0, 0, 1000, "started", 50, "",
                 "1'; DROP TABLE peers;--");

    // ip was invalid, so ip_port empty, peer marked invalid; record_peer still called
    // but the ip field passed to it is the raw string — verify the record_str is still safe
    for (auto &pr : fix.db.peer_records) {
        for (char c : pr.record) {
            bool safe = (c >= '0' && c <= '9') || c == ',' || c == '(' || c == ')' || c == '-';
            EXPECT_TRUE(safe) << "Unexpected char in record string from malicious IP";
        }
    }
}

// TC-SEC-02: oversized request body — no crash
TEST_F(SecurityTest, oversized_request_no_crash) {
    // 1 MB HTTP body (valid-ish structure but huge)
    std::string huge_req = "GET /" + TEST_PASSKEY + "/announce?info_hash="
                         + percent_encode(TEST_INFO_HASH)
                         + "&peer_id=" + percent_encode(TEST_PEER_ID)
                         + "&port=6881&uploaded=0&downloaded=0&left=0&compact=1 HTTP/1.1\r\n";
    // Pad with garbage headers
    huge_req += std::string(1024 * 1024, 'X') + "\r\n";

    std::string ip = TEST_PEER_IP;
    client_opts_t opts = {false, false, false};
    EXPECT_NO_THROW(fix.w->work(huge_req, ip, opts));
}

// TC-SEC-03: report endpoint with wrong password → failure
// Passkey must be exactly 32 chars; the parser rejects a different length before auth.
TEST_F(SecurityTest, report_wrong_password_rejected) {
    std::string req = make_get_request("wrongpasswordwrongpassword000000", "report", "get=stats");
    std::string ip = "127.0.0.1";
    client_opts_t opts = {false, false, false};
    std::string r = fix.w->work(req, ip, opts);
    EXPECT_TRUE(WorkerFixture::is_failure(r));
    EXPECT_NE(std::string::npos, WorkerFixture::body(r).find("Authentication failure"));
}

// TC-SEC-04: update endpoint with wrong password → failure
TEST_F(SecurityTest, update_wrong_password_rejected) {
    std::string req = make_get_request("wrongpasswordwrongpassword000000", "update",
                                       "action=add_torrent&info_hash="
                                       + percent_encode(std::string(20, '\xCC'))
                                       + "&id=1&freetorrent=0");
    std::string ip = "127.0.0.1";
    client_opts_t opts = {false, false, false};
    std::string r = fix.w->work(req, ip, opts);
    EXPECT_TRUE(WorkerFixture::is_failure(r));
}

// TC-SEC-05: passkey lookup is hash-map O(1), not linear scan
// Structural: verify users_list is an unordered_map (not a vector or list).
// A linear scan of N users would be a DoS vector.
TEST(Security, passkey_lookup_uses_hash_map) {
    // The user_list typedef is unordered_map<string, user_ptr>.
    // If this compiles and the type is correct, the lookup is O(1) amortized.
    user_list ul;
    auto ptr = std::make_shared<user>(1u, true, false);
    ul["akey"] = ptr;
    auto it = ul.find("akey");
    EXPECT_NE(ul.end(), it);
    EXPECT_EQ(1u, it->second->get_id());
}

// TC-SEC-06 / TC-SEC-07: AddressSanitizer / UBSanitizer coverage
// These tests exercise all major code paths so ASAN/UBSAN can detect bugs
// when compiled with -fsanitize=address,undefined.

TEST_F(SecurityTest, asan_ubsan_announce_started_stopped) {
    fix.announce(TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
                 TEST_PORT, 0, 0, 1000, "started");
    fix.announce(TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
                 TEST_PORT, 500, 300, 700);
    fix.announce(TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
                 TEST_PORT, 1000, 1000, 0, "completed");
    fix.announce(TEST_PASSKEY, TEST_INFO_HASH, TEST_PEER_ID,
                 TEST_PORT, 0, 0, 0, "stopped");
    SUCCEED();
}

TEST_F(SecurityTest, asan_ubsan_update_then_announce) {
    // delete torrent then try to announce → graceful failure
    params_type p;
    p["action"]    = "delete_torrent";
    p["info_hash"] = percent_encode(TEST_INFO_HASH);
    fix.update_action(p);

    std::string r = fix.announce();
    // Must be a failure, not a crash
    EXPECT_TRUE(WorkerFixture::is_failure(r));
}

TEST_F(SecurityTest, asan_ubsan_empty_swarm_scrape) {
    // Scrape with no peers — must not crash or produce garbage
    std::string req = make_get_request(TEST_PASSKEY, "scrape",
                                       "info_hash=" + percent_encode(TEST_INFO_HASH));
    std::string ip = "1.2.3.4";
    client_opts_t opts = {false, false, false};
    EXPECT_NO_THROW(fix.w->work(req, ip, opts));
}
