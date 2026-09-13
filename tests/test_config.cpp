// Tier 1 — TC-CF-01 through TC-CF-09
#include <gtest/gtest.h>
#include <sstream>
#include <fstream>
#include <cstdio>
#include "config.h"

// TC-CF-01: defaults after init()
TEST(Config, defaults_set_after_construction) {
    config c;
    EXPECT_EQ(34000u,  c.get_uint("listen_port"));
    EXPECT_EQ(1800u,   c.get_uint("announce_interval"));
    EXPECT_EQ(1024u,   c.get_uint("max_connections"));
    EXPECT_EQ(50u,     c.get_uint("numwant_limit"));
    EXPECT_EQ(false,   c.get_bool("readonly"));
    EXPECT_EQ("gazelle", c.get_str("mysql_db"));
}

// TC-CF-02: parse valid key=value
TEST(Config, parse_valid_key_value) {
    config c;
    std::istringstream ss("listen_port=7070\n");
    c.load(ss);
    EXPECT_EQ(7070u, c.get_uint("listen_port"));
}

TEST(Config, parse_with_whitespace) {
    config c;
    std::istringstream ss("  listen_port  =  9999  \n");
    c.load(ss);
    EXPECT_EQ(9999u, c.get_uint("listen_port"));
}

// TC-CF-03: boolean parsing
TEST(Config, bool_parse_true_variants) {
    config c;
    std::istringstream ss("readonly=1\n");
    c.load(ss);
    EXPECT_TRUE(c.get_bool("readonly"));

    config c2;
    std::istringstream ss2("readonly=true\n");
    c2.load(ss2);
    EXPECT_TRUE(c2.get_bool("readonly"));

    config c3;
    std::istringstream ss3("readonly=yes\n");
    c3.load(ss3);
    EXPECT_TRUE(c3.get_bool("readonly"));
}

TEST(Config, bool_parse_false_variants) {
    config c;
    std::istringstream ss("readonly=0\n");
    c.load(ss);
    EXPECT_FALSE(c.get_bool("readonly"));

    config c2;
    std::istringstream ss2("readonly=false\n");
    c2.load(ss2);
    EXPECT_FALSE(c2.get_bool("readonly"));

    config c3;
    std::istringstream ss3("readonly=no\n");
    c3.load(ss3);
    EXPECT_FALSE(c3.get_bool("readonly"));
}

// TC-CF-04: comment stripping
TEST(Config, comments_ignored) {
    config c;
    std::istringstream ss("# listen_port=9999\nlisten_port=1234\n");
    c.load(ss);
    EXPECT_EQ(1234u, c.get_uint("listen_port"));
}

// TC-CF-05: missing key returns default
TEST(Config, missing_key_returns_default) {
    config c;
    // "listen_port" default is 34000; never loaded differently
    EXPECT_EQ(34000u, c.get_uint("listen_port"));
}

// TC-CF-06: set() runtime override persists
TEST(Config, set_runtime_override) {
    config c;
    c.set("listen_port", "12345");
    EXPECT_EQ(12345u, c.get_uint("listen_port"));
    // Survives a second get
    EXPECT_EQ(12345u, c.get_uint("listen_port"));
}

// TC-CF-07: reload() re-reads file
TEST(Config, reload_rereads_file) {
    // Write a temp file
    const char *tmppath = "/tmp/ocelot_test_config.conf";
    {
        std::ofstream f(tmppath);
        f << "listen_port=1111\n";
    }
    std::ifstream first_load(tmppath);
    config c;
    c.load(tmppath, first_load);
    EXPECT_EQ(1111u, c.get_uint("listen_port"));

    // Overwrite the file
    {
        std::ofstream f(tmppath);
        f << "listen_port=2222\n";
    }
    c.reload();
    EXPECT_EQ(2222u, c.get_uint("listen_port"));

    std::remove(tmppath);
}

// TC-CF-07b: reload() without file path: deleted key reverts to default
TEST(Config, reload_reverts_deleted_key_to_default) {
    const char *tmppath = "/tmp/ocelot_test_config2.conf";
    {
        std::ofstream f(tmppath);
        f << "listen_port=9999\n";
    }
    std::ifstream first_load(tmppath);
    config c;
    c.load(tmppath, first_load);
    EXPECT_EQ(9999u, c.get_uint("listen_port"));

    // Write a file that does NOT set listen_port
    {
        std::ofstream f(tmppath);
        f << "announce_interval=600\n";
    }
    c.reload();
    EXPECT_EQ(34000u, c.get_uint("listen_port")); // reverts to default
    EXPECT_EQ(600u,   c.get_uint("announce_interval"));

    std::remove(tmppath);
}

// TC-CF-08: malformed line (no '=') does not crash, is skipped
TEST(Config, malformed_line_skipped) {
    config c;
    std::istringstream ss("noequalssign\nlisten_port=5555\n");
    EXPECT_NO_THROW(c.load(ss));
    EXPECT_EQ(5555u, c.get_uint("listen_port"));
}

// TC-CF-09: empty file — all defaults remain intact
TEST(Config, empty_file_defaults_intact) {
    config c;
    std::istringstream ss("");
    c.load(ss);
    EXPECT_EQ(34000u, c.get_uint("listen_port"));
    EXPECT_EQ(1800u,  c.get_uint("announce_interval"));
    EXPECT_EQ(false,  c.get_bool("readonly"));
}
