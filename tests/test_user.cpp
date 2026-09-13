// Tier 1 — TC-US-01 through TC-US-05
// Tier 3 — TC-MT-05 (atomic counter race)
#include <gtest/gtest.h>
#include <thread>
#include <vector>
#include "user.h"

// TC-US-01: construction defaults
TEST(User, default_counters_zero) {
    user u(42u, true, false);
    EXPECT_EQ(0u, u.get_leeching());
    EXPECT_EQ(0u, u.get_seeding());
    EXPECT_EQ(42u, u.get_id());
    EXPECT_FALSE(u.is_deleted());
}

// TC-US-02: increment leeching / seeding
TEST(User, increment_counters) {
    user u(1u, true, false);
    u.incr_leeching();
    EXPECT_EQ(1u, u.get_leeching());
    u.incr_leeching();
    EXPECT_EQ(2u, u.get_leeching());

    u.incr_seeding();
    EXPECT_EQ(1u, u.get_seeding());

    u.decr_leeching();
    EXPECT_EQ(1u, u.get_leeching());
    u.decr_seeding();
    EXPECT_EQ(0u, u.get_seeding());
}

// TC-US-03: concurrent increments — no race, correct final count
// Run with -fsanitize=thread to detect data races.
TEST(User, concurrent_increments) {
    user u(1u, true, false);
    const int THREADS = 100;
    const int OPS     = 1000;

    std::vector<std::thread> threads;
    threads.reserve(THREADS);
    for (int i = 0; i < THREADS; i++) {
        threads.emplace_back([&u, OPS](){
            for (int j = 0; j < OPS; j++) u.incr_leeching();
        });
    }
    for (auto &t : threads) t.join();

    EXPECT_EQ(static_cast<uint32_t>(THREADS * OPS), u.get_leeching());
}

// TC-MT-05: symmetric increment+decrement reaches zero with no data race
TEST(User, concurrent_increment_decrement_net_zero) {
    user u(1u, true, false);
    const int HALF    = 50;
    const int OPS     = 1000;

    std::vector<std::thread> threads;
    threads.reserve(HALF * 2);
    for (int i = 0; i < HALF; i++) {
        threads.emplace_back([&u, OPS](){
            for (int j = 0; j < OPS; j++) u.incr_leeching();
        });
        threads.emplace_back([&u, OPS](){
            for (int j = 0; j < OPS; j++) u.decr_leeching();
        });
    }
    for (auto &t : threads) t.join();

    EXPECT_EQ(0u, u.get_leeching());
}

// TC-US-04: can_leech flag
TEST(User, leech_disabled) {
    user u(1u, false /*leech*/, false);
    EXPECT_FALSE(u.can_leech());
    u.set_leechstatus(true);
    EXPECT_TRUE(u.can_leech());
}

// TC-US-05: deleted flag
TEST(User, deleted_flag) {
    user u(1u, true, false);
    EXPECT_FALSE(u.is_deleted());
    u.set_deleted(true);
    EXPECT_TRUE(u.is_deleted());
    u.set_deleted(false);
    EXPECT_FALSE(u.is_deleted());
}

// protect_ip flag
TEST(User, protect_ip_flag) {
    user u(1u, true, true);
    EXPECT_TRUE(u.is_protected());
    u.set_protected(false);
    EXPECT_FALSE(u.is_protected());
}
