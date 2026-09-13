#pragma once
#include <string>
#include <vector>
#include "db_interface.h"

// Test double for db_interface.
// Records all calls for assertion; performs no I/O.
class MockDB : public db_interface {
public:
    std::vector<std::string> recorded_users;
    std::vector<std::string> recorded_torrents;
    std::vector<std::string> recorded_peers;
    std::vector<std::string> recorded_snatches;
    std::vector<std::string> recorded_tokens;

    // Per-call recording: peer records come in two overloads, track separately
    struct PeerRecord {
        std::string record;
        std::string ip;
        std::string peer_id;
        std::string useragent;
        bool heavy; // true = heavy (4-arg) overload
    };
    std::vector<PeerRecord> peer_records;

    void reset() {
        recorded_users.clear();
        recorded_torrents.clear();
        recorded_peers.clear();
        recorded_snatches.clear();
        recorded_tokens.clear();
        peer_records.clear();
    }

    void load_torrents(torrent_list &) override {}
    void load_users(user_list &) override {}
    void load_whitelist(std::vector<std::string> &) override {}

    void record_user(const std::string &r) override {
        recorded_users.push_back(r);
    }

    void record_torrent(const std::string &r) override {
        recorded_torrents.push_back(r);
    }

    void record_snatch(const std::string &r, const std::string &ip) override {
        recorded_snatches.push_back(r);
        (void)ip;
    }

    void record_peer(const std::string &r, const std::string &ip,
                     const std::string &peer_id, const std::string &useragent) override {
        recorded_peers.push_back(r);
        peer_records.push_back({r, ip, peer_id, useragent, true});
    }

    void record_peer(const std::string &r, const std::string &peer_id) override {
        recorded_peers.push_back(r);
        peer_records.push_back({r, "", peer_id, "", false});
    }

    void record_token(const std::string &r) override {
        recorded_tokens.push_back(r);
    }

    void flush() override {}
    bool all_clear() override { return true; }
};
