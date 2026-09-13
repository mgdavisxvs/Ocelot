#pragma once
#include "site_comm_interface.h"
#include <vector>
#include <utility>

// Test double for site_comm_interface.
// Records expire_token calls for assertion; performs no I/O.
class MockSiteComm : public site_comm_interface {
public:
    std::vector<std::pair<int,int>> expired_tokens; // (torrent_id, user_id)

    void reset() { expired_tokens.clear(); }

    void expire_token(int torrent_id, int user_id) override {
        expired_tokens.push_back({torrent_id, user_id});
    }

    void flush_tokens() override {}
    bool all_clear() override { return true; }
};
