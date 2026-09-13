#pragma once
#include <string>
#include <vector>
#include <mutex>
#include "ocelot.h"

// Abstract interface for the database layer.
// Allows test doubles to replace the MySQL-dependent mysql class
// without pulling MySQL++ headers into the test compilation units.
class db_interface {
public:
    std::mutex torrent_list_mutex;
    std::mutex user_list_mutex;
    std::mutex whitelist_mutex;

    virtual ~db_interface() = default;

    virtual void load_torrents(torrent_list &torrents) = 0;
    virtual void load_users(user_list &users) = 0;
    virtual void load_whitelist(std::vector<std::string> &whitelist) = 0;

    virtual void record_user(const std::string &record) = 0;
    virtual void record_torrent(const std::string &record) = 0;
    virtual void record_snatch(const std::string &record, const std::string &ip) = 0;
    virtual void record_peer(const std::string &record, const std::string &ip,
                              const std::string &peer_id, const std::string &useragent) = 0;
    virtual void record_peer(const std::string &record, const std::string &peer_id) = 0;
    virtual void record_token(const std::string &record) = 0;

    virtual void flush() = 0;
    virtual bool all_clear() = 0;
};
