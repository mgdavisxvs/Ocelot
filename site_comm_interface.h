#pragma once

// Abstract interface for site communication layer.
// Allows test doubles to replace the Boost.Asio-dependent site_comm class
// without pulling boost headers into the test compilation units.
class site_comm_interface {
public:
    virtual ~site_comm_interface() = default;

    virtual void expire_token(int torrent_id, int user_id) = 0;
    virtual void flush_tokens() = 0;
    virtual bool all_clear() = 0;
};
