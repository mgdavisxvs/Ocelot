#pragma once
#include <string>
#include <memory>
#include <sstream>
#include "ocelot.h"
#include "user.h"
#include "config.h"
#include "misc_functions.h"
#include "worker.h"
#include "mock_db.h"
#include "mock_site_comm.h"

// ---- Constants ----

// A 32-char hex passkey used in all worker tests.
static const std::string TEST_PASSKEY    = "abcdef1234567890abcdef12345678ab";
// A second passkey for a seeder user.
static const std::string TEST_PASSKEY2   = "11111111111111111111111111111111";

// 20-byte binary info_hash (all 0x01 bytes); URL-encoded as "%01" x 20.
static const std::string TEST_INFO_HASH  = std::string(20, '\x01');

// 20-byte binary peer_id (all 0x02 bytes).
static const std::string TEST_PEER_ID   = std::string(20, '\x02');
static const std::string TEST_PEER_ID2  = std::string(20, '\x03');

static const std::string TEST_PEER_IP   = "1.2.3.4";
static const uint16_t    TEST_PORT      = 6881;

// Site / update password (must be exactly what config has).
static const std::string SITE_PASS      = "sitepass1234567890123456789012ab";
static const std::string REPORT_PASS    = "reppass12345678901234567890123ab";

// ---- Encoding helpers ----

inline std::string percent_encode(const std::string &s) {
    std::string out;
    out.reserve(s.size() * 3);
    for (unsigned char c : s) {
        char buf[4];
        snprintf(buf, sizeof(buf), "%%%02X", static_cast<unsigned>(c));
        out += buf;
    }
    return out;
}

// ---- HTTP request builders ----

// Build a minimal valid HTTP/1.1 GET request for Ocelot's parser.
// passkey must be exactly 32 chars; action is e.g. "announce", "scrape", "update"
inline std::string make_get_request(
    const std::string &passkey,
    const std::string &action,
    const std::string &query,      // everything after '?'
    const std::string &extra_hdrs = "",
    const std::string &http_ver   = "1.1"
) {
    std::string req = "GET /" + passkey + "/" + action + "?" + query
                    + " HTTP/" + http_ver + "\r\n"
                    + extra_hdrs + "\r\n";
    return req;
}

inline std::string make_announce_query(
    const std::string &info_hash_encoded,
    const std::string &peer_id_encoded,
    int port               = TEST_PORT,
    int64_t uploaded       = 0,
    int64_t downloaded     = 0,
    int64_t left           = 0,
    const std::string &event = "",
    int numwant            = 50,
    const std::string &ip  = ""
) {
    std::ostringstream q;
    q << "info_hash=" << info_hash_encoded
      << "&peer_id="  << peer_id_encoded
      << "&port="     << port
      << "&uploaded=" << uploaded
      << "&downloaded=" << downloaded
      << "&left="     << left
      << "&compact=1";
    if (!event.empty())  q << "&event=" << event;
    if (numwant != 50)   q << "&numwant=" << numwant;
    if (!ip.empty())     q << "&ip=" << ip;
    return q.str();
}

inline std::string make_update_query(const params_type &p) {
    std::string q;
    bool first = true;
    for (auto &kv : p) {
        if (!first) q += '&';
        q += kv.first + "=" + kv.second;
        first = false;
    }
    return q;
}

// ---- WorkerFixture ----

struct WorkerFixture {
    torrent_list torrents;
    user_list    users;
    std::vector<std::string> whitelist;
    config       conf;
    MockDB       db;
    MockSiteComm sc;
    std::unique_ptr<worker> w;

    // Default test user (leeching allowed, not IP-protected).
    user_ptr user1;
    user_ptr user2;

    WorkerFixture() {
        conf.set("site_password",   SITE_PASS);
        conf.set("report_password", REPORT_PASS);
        conf.set("announce_interval",  "1800");
        conf.set("numwant_limit",      "50");
        conf.set("peers_timeout",      "7200");
        conf.set("del_reason_lifetime","86400");

        user1 = std::make_shared<user>(1u, true,  false);
        user2 = std::make_shared<user>(2u, true,  false);
        users[TEST_PASSKEY]  = user1;
        users[TEST_PASSKEY2] = user2;

        torrent t;
        t.id = 1;
        t.balance = 0;
        t.completed = 0;
        t.free_torrent = NORMAL;
        t.last_selected_seeder = "";
        torrents[TEST_INFO_HASH] = t;

        w = std::make_unique<worker>(&conf, torrents, users, whitelist, &db, &sc);
    }

    // Check whether a bencode response contains a "failure reason" key.
    static bool is_failure(const std::string &resp) {
        // Response body starts after the HTTP header (double CRLF).
        auto pos = resp.find("\r\n\r\n");
        std::string body = (pos != std::string::npos) ? resp.substr(pos + 4) : resp;
        return body.find("failure reason") != std::string::npos;
    }

    // Extract the response body (after HTTP headers).
    static std::string body(const std::string &resp) {
        auto pos = resp.find("\r\n\r\n");
        return (pos != std::string::npos) ? resp.substr(pos + 4) : resp;
    }

    // Extract the raw bencode "peers" value from a successful announce response.
    // Returns the binary peer string only (no length prefix, no trailing 'e').
    static std::string extract_peers(const std::string &resp) {
        std::string b = body(resp);
        auto pos = b.find("5:peers");
        if (pos == std::string::npos) return "";
        pos += 7; // skip "5:peers"
        // Next comes "<len>:<data>"
        size_t colon = b.find(':', pos);
        if (colon == std::string::npos) return "";
        size_t len = std::stoul(b.substr(pos, colon - pos));
        if (colon + 1 + len > b.size()) return "";
        return b.substr(colon + 1, len);
    }

    // Build a compact peer string: 4-byte big-endian IPv4 + 2-byte big-endian port.
    static std::string compact_peer(const std::string &ip, uint16_t port) {
        std::string result(6, '\0');
        unsigned int a, b, c, d;
        sscanf(ip.c_str(), "%u.%u.%u.%u", &a, &b, &c, &d);
        result[0] = (char)a; result[1] = (char)b;
        result[2] = (char)c; result[3] = (char)d;
        result[4] = (char)(port >> 8);
        result[5] = (char)(port & 0xFF);
        return result;
    }

    std::string announce(
        const std::string &passkey      = TEST_PASSKEY,
        const std::string &info_hash    = TEST_INFO_HASH,
        const std::string &peer_id      = TEST_PEER_ID,
        int port                        = TEST_PORT,
        int64_t uploaded                = 0,
        int64_t downloaded              = 0,
        int64_t left                    = 0,
        const std::string &event        = "",
        int numwant                     = 50,
        const std::string &extra_hdrs   = "",
        const std::string &peer_ip      = TEST_PEER_IP
    ) {
        std::string query = make_announce_query(
            percent_encode(info_hash),
            percent_encode(peer_id),
            port, uploaded, downloaded, left, event, numwant, peer_ip
        );
        std::string req = make_get_request(passkey, "announce", query, extra_hdrs);
        std::string ip = peer_ip;
        client_opts_t opts = {false, false, false};
        return w->work(req, ip, opts);
    }

    std::string update_action(params_type params) {
        std::string query = make_update_query(params);
        std::string req = make_get_request(SITE_PASS, "update", query);
        std::string ip = "127.0.0.1";
        client_opts_t opts = {false, false, false};
        return w->work(req, ip, opts);
    }
};
