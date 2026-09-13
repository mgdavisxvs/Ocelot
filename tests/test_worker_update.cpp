// Tier 2 — TC-WK-UP-01 through TC-WK-UP-10
#include <gtest/gtest.h>
#include "test_helpers.h"

class UpdateTest : public ::testing::Test {
protected:
    WorkerFixture fix;

    std::string update(params_type params) {
        return fix.update_action(params);
    }

    bool is_success(const std::string &r) {
        return WorkerFixture::body(r).find("success") != std::string::npos;
    }
};

// TC-WK-UP-01: wrong update_password → failure
TEST_F(UpdateTest, wrong_password_rejected) {
    std::string query = "action=add_torrent&info_hash="
                      + percent_encode(std::string(20, '\xAA'))
                      + "&id=99&freetorrent=0";
    std::string req = make_get_request("wrongpasswordwrongpasswordwrong!!", "update", query);
    std::string ip = "127.0.0.1";
    client_opts_t opts = {false, false, false};
    std::string r = fix.w->work(req, ip, opts);
    EXPECT_TRUE(WorkerFixture::is_failure(r));
}

// TC-WK-UP-02: add_torrent — torrent appears in torrent_list
TEST_F(UpdateTest, add_torrent) {
    std::string new_hash(20, '\xAA');
    params_type p;
    p["action"]      = "add_torrent";
    p["info_hash"]   = percent_encode(new_hash);
    p["id"]          = "99";
    p["freetorrent"] = "0";
    std::string r = update(p);
    EXPECT_TRUE(is_success(r));
    EXPECT_NE(fix.torrents.end(), fix.torrents.find(new_hash));
    EXPECT_EQ(99u, fix.torrents[new_hash].id);
    EXPECT_EQ(NORMAL, fix.torrents[new_hash].free_torrent);
}

TEST_F(UpdateTest, add_torrent_free) {
    std::string new_hash(20, '\xBB');
    params_type p;
    p["action"]      = "add_torrent";
    p["info_hash"]   = percent_encode(new_hash);
    p["id"]          = "100";
    p["freetorrent"] = "1";
    update(p);
    EXPECT_EQ(FREE, fix.torrents[new_hash].free_torrent);
}

// TC-WK-UP-03: delete_torrent — torrent removed from torrent_list
TEST_F(UpdateTest, delete_torrent) {
    params_type p;
    p["action"]    = "delete_torrent";
    p["info_hash"] = percent_encode(TEST_INFO_HASH);
    p["reason"]    = "0"; // DUPE
    std::string r = update(p);
    EXPECT_TRUE(is_success(r));
    EXPECT_EQ(fix.torrents.end(), fix.torrents.find(TEST_INFO_HASH));
}

// TC-WK-UP-04: update_torrent free_torrent flag changed in-memory
TEST_F(UpdateTest, update_torrent_free_flag) {
    params_type p;
    p["action"]      = "update_torrent";
    p["info_hash"]   = percent_encode(TEST_INFO_HASH);
    p["freetorrent"] = "1"; // FREE
    std::string r = update(p);
    EXPECT_TRUE(is_success(r));
    EXPECT_EQ(FREE, fix.torrents[TEST_INFO_HASH].free_torrent);
}

TEST_F(UpdateTest, update_torrent_neutral_flag) {
    params_type p;
    p["action"]      = "update_torrent";
    p["info_hash"]   = percent_encode(TEST_INFO_HASH);
    p["freetorrent"] = "2"; // NEUTRAL
    update(p);
    EXPECT_EQ(NEUTRAL, fix.torrents[TEST_INFO_HASH].free_torrent);
}

// TC-WK-UP-05: add_user — user appears in user_list
TEST_F(UpdateTest, add_user) {
    std::string new_passkey = "99999999999999999999999999999999";
    params_type p;
    p["action"]  = "add_user";
    p["passkey"] = new_passkey;
    p["id"]      = "50";
    p["visible"] = "1";
    std::string r = update(p);
    EXPECT_TRUE(is_success(r));
    auto it = fix.users.find(new_passkey);
    ASSERT_NE(fix.users.end(), it);
    EXPECT_EQ(50u, it->second->get_id());
}

// TC-WK-UP-06: remove_user — user removed from user_list
TEST_F(UpdateTest, remove_user) {
    params_type p;
    p["action"]  = "remove_user";
    p["passkey"] = TEST_PASSKEY;
    std::string r = update(p);
    EXPECT_TRUE(is_success(r));
    EXPECT_EQ(fix.users.end(), fix.users.find(TEST_PASSKEY));
}

// TC-WK-UP-07: change_passkey — new key resolves to same user
TEST_F(UpdateTest, change_passkey) {
    std::string new_passkey = "newnewnewnewnewnewnewnewnewnewne";
    params_type p;
    p["action"]     = "change_passkey";
    p["oldpasskey"] = TEST_PASSKEY;
    p["newpasskey"] = new_passkey;
    userid_t old_id = fix.user1->get_id();
    std::string r = update(p);
    EXPECT_TRUE(is_success(r));

    // Old passkey gone
    EXPECT_EQ(fix.users.end(), fix.users.find(TEST_PASSKEY));
    // New passkey resolves to same user id
    auto it = fix.users.find(new_passkey);
    ASSERT_NE(fix.users.end(), it);
    EXPECT_EQ(old_id, it->second->get_id());
}

// TC-WK-UP-08: add_whitelist — prefix present in whitelist
TEST_F(UpdateTest, add_whitelist) {
    params_type p;
    p["action"]  = "add_whitelist";
    p["peer_id"] = "-UT3";
    std::string r = update(p);
    EXPECT_TRUE(is_success(r));
    bool found = false;
    for (auto &s : fix.whitelist) {
        if (s == "-UT3") { found = true; break; }
    }
    EXPECT_TRUE(found);
}

// TC-WK-UP-09: remove_whitelist — prefix removed
TEST_F(UpdateTest, remove_whitelist) {
    fix.whitelist.push_back("-UT3");

    params_type p;
    p["action"]  = "remove_whitelist";
    p["peer_id"] = "-UT3";
    update(p);

    for (auto &s : fix.whitelist) {
        EXPECT_NE(s, "-UT3");
    }
}

// TC-WK-UP-10: update_announce_interval — config value updated; response reflects it
TEST_F(UpdateTest, update_announce_interval) {
    params_type p;
    p["action"]                = "update_announce_interval";
    p["new_announce_interval"] = "900";
    update(p);

    // Now announce and check the interval in the response
    std::string r = fix.announce();
    std::string b = WorkerFixture::body(r);
    // interval key should now be ~900 (plus seeder adjustment)
    EXPECT_NE(std::string::npos, b.find("8:intervali9"));
}

// add_token and remove_token
TEST_F(UpdateTest, add_and_remove_token) {
    // Add token for user1 on test torrent
    params_type p_add;
    p_add["action"]    = "add_token";
    p_add["info_hash"] = percent_encode(TEST_INFO_HASH);
    p_add["userid"]    = "1";
    update(p_add);
    EXPECT_NE(fix.torrents[TEST_INFO_HASH].tokened_users.end(),
              fix.torrents[TEST_INFO_HASH].tokened_users.find(1));

    // Remove it
    params_type p_rm;
    p_rm["action"]    = "remove_token";
    p_rm["info_hash"] = percent_encode(TEST_INFO_HASH);
    p_rm["userid"]    = "1";
    update(p_rm);
    EXPECT_EQ(fix.torrents[TEST_INFO_HASH].tokened_users.end(),
              fix.torrents[TEST_INFO_HASH].tokened_users.find(1));
}

// update_user: can_leech and protect_ip flags
TEST_F(UpdateTest, update_user_flags) {
    params_type p;
    p["action"]    = "update_user";
    p["passkey"]   = TEST_PASSKEY;
    p["can_leech"] = "0";
    p["visible"]   = "0";
    update(p);

    EXPECT_FALSE(fix.user1->can_leech());
    EXPECT_TRUE(fix.user1->is_protected());
}
