// Tier 2 — TC-WK-SC-01 through TC-WK-SC-04
// Tier 5 — BEP-48 scrape conformance
#include <gtest/gtest.h>
#include "test_helpers.h"

class ScrapeTest : public ::testing::Test {
protected:
    WorkerFixture fix;

    std::string do_scrape(const std::string &info_hash_encoded, const std::string &extra = "") {
        std::string passkey = TEST_PASSKEY;
        std::string query = "info_hash=" + info_hash_encoded + extra;
        std::string req = make_get_request(passkey, "scrape", query);
        std::string ip = "1.2.3.4";
        client_opts_t opts = {false, false, false};
        return fix.w->work(req, ip, opts);
    }

    std::string body(const std::string &r) { return WorkerFixture::body(r); }
};

// TC-WK-SC-01: single info_hash — response "files" dict has that hash
TEST_F(ScrapeTest, single_info_hash_in_files_dict) {
    std::string r = do_scrape(percent_encode(TEST_INFO_HASH));
    std::string b = body(r);

    // Must contain "files" key
    EXPECT_NE(std::string::npos, b.find("5:filesd"));

    // Must contain "complete" and "incomplete" keys
    EXPECT_NE(std::string::npos, b.find("8:completei"));
    EXPECT_NE(std::string::npos, b.find("10:incompletei"));
}

// TC-WK-SC-02: multiple info_hashes — all returned
TEST_F(ScrapeTest, multiple_info_hashes_all_returned) {
    // Add a second torrent
    torrent t2;
    t2.id = 2;
    t2.balance = 0;
    t2.completed = 0;
    t2.free_torrent = NORMAL;
    t2.last_selected_seeder = "";
    std::string hash2(20, '\x02');
    fix.torrents[hash2] = t2;

    std::string query = "info_hash=" + percent_encode(TEST_INFO_HASH)
                      + "&info_hash=" + percent_encode(hash2);
    std::string req = make_get_request(TEST_PASSKEY, "scrape", query);
    std::string ip = "1.2.3.4";
    client_opts_t opts = {false, false, false};
    std::string r = fix.w->work(req, ip, opts);
    std::string b = body(r);

    // Both hashes should appear (each followed by their dict)
    // At minimum, two "8:completei" entries should appear
    size_t pos1 = b.find("8:completei");
    EXPECT_NE(std::string::npos, pos1);
    size_t pos2 = b.find("8:completei", pos1 + 1);
    EXPECT_NE(std::string::npos, pos2);
}

// TC-WK-SC-03: unknown info_hash — omitted from response (no error)
TEST_F(ScrapeTest, unknown_info_hash_omitted) {
    std::string bad_hash(20, '\xFF');
    std::string r = do_scrape(percent_encode(bad_hash));
    std::string b = body(r);

    // No failure reason
    EXPECT_EQ(std::string::npos, b.find("14:failure reason"));

    // "files" dict is empty → "d5:filesdeee" or "d5:filesd" immediately followed by end
    EXPECT_NE(std::string::npos, b.find("5:files"));
    // The torrent dict for the unknown hash should NOT appear
    // (no "8:completei" entry for an unknown hash)
}

// TC-WK-SC-04: empty scrape (no info_hash params) — "files" dict is empty
TEST_F(ScrapeTest, empty_scrape_no_info_hash) {
    std::string req = make_get_request(TEST_PASSKEY, "scrape", "x=y");
    std::string ip = "1.2.3.4";
    client_opts_t opts = {false, false, false};
    std::string r = fix.w->work(req, ip, opts);
    std::string b = body(r);

    EXPECT_NE(std::string::npos, b.find("5:files"));
    // Should be empty files dict: no complete/incomplete entries
}

// BEP-48: scrape response keys
// TC-BEP48-01: "files" dict with 20-byte binary keys
TEST_F(ScrapeTest, bep48_files_dict_binary_keys) {
    std::string r = do_scrape(percent_encode(TEST_INFO_HASH));
    std::string b = body(r);

    // files dict should contain the binary info_hash as a key
    // bencode key format: "20:<binary>"
    std::string expected_key = "20:" + TEST_INFO_HASH;
    EXPECT_NE(std::string::npos, b.find(expected_key));
}

// TC-BEP48-02: per-torrent dict has "complete", "incomplete", "downloaded" keys
TEST_F(ScrapeTest, bep48_per_torrent_dict_keys) {
    std::string r = do_scrape(percent_encode(TEST_INFO_HASH));
    std::string b = body(r);

    EXPECT_NE(std::string::npos, b.find("8:completei"));
    EXPECT_NE(std::string::npos, b.find("10:incompletei"));
    EXPECT_NE(std::string::npos, b.find("10:downloadedi"));
}

// Seeder/leecher counts are accurate
TEST_F(ScrapeTest, scrape_counts_reflect_peers) {
    // Add a seeder and a leecher
    fix.announce(TEST_PASSKEY,  TEST_INFO_HASH, TEST_PEER_ID,
                 6881, 0, 0, 0, "started", 50, "", "10.0.0.1");
    fix.announce(TEST_PASSKEY2, TEST_INFO_HASH, TEST_PEER_ID2,
                 6882, 0, 0, 1000, "started", 50, "", "10.0.0.2");

    std::string r = do_scrape(percent_encode(TEST_INFO_HASH));
    std::string b = body(r);

    // complete = 1 seeder, incomplete = 1 leecher
    EXPECT_NE(std::string::npos, b.find("8:completei1e"));
    EXPECT_NE(std::string::npos, b.find("10:incompletei1e"));
}
