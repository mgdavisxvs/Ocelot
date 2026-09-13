// Tier 1 — TC-RS-01 through TC-RS-07
#include <gtest/gtest.h>
#include <sstream>
#include <boost/iostreams/filtering_streambuf.hpp>
#include <boost/iostreams/copy.hpp>
#include <boost/iostreams/filter/gzip.hpp>
#include "response.h"
#include "ocelot.h"

static client_opts_t make_opts(bool gzip = false, bool html = false, bool close = false) {
    return {gzip, html, close};
}

static std::string header_of(const std::string &resp) {
    auto pos = resp.find("\r\n\r\n");
    return (pos != std::string::npos) ? resp.substr(0, pos) : resp;
}

static std::string body_of(const std::string &resp) {
    auto pos = resp.find("\r\n\r\n");
    return (pos != std::string::npos) ? resp.substr(pos + 4) : "";
}

// TC-RS-01: plain response — HTTP/1.1 200 OK, correct content-type and content-length
TEST(Response, plain_response_headers) {
    auto opts = make_opts();
    std::string resp = response("hello", opts);
    std::string hdr  = header_of(resp);
    EXPECT_NE(std::string::npos, hdr.find("HTTP/1.1 200 OK"));
    EXPECT_NE(std::string::npos, hdr.find("Content-Type: text/plain"));
    EXPECT_NE(std::string::npos, hdr.find("Content-Length: 5")); // "hello" = 5
    EXPECT_EQ("hello", body_of(resp));
}

// TC-RS-02: error() produces bencoded failure reason
TEST(Response, error_bencode_format) {
    auto opts = make_opts();
    std::string resp = error("oops", opts);
    std::string b    = body_of(resp);
    // Must contain bencode dict with "failure reason" key and the error string
    EXPECT_NE(std::string::npos, b.find("14:failure reason"));
    EXPECT_NE(std::string::npos, b.find("4:oops"));
}

TEST(Response, error_includes_interval_keys) {
    auto opts = make_opts();
    std::string b = body_of(error("x", opts));
    EXPECT_NE(std::string::npos, b.find("8:interval"));
    EXPECT_NE(std::string::npos, b.find("12:min interval"));
}

// TC-RS-03: warning() produces bencoded warning message (not a full HTTP response)
TEST(Response, warning_bencode_format) {
    std::string w = warning("warn_msg");
    EXPECT_NE(std::string::npos, w.find("15:warning message"));
    EXPECT_NE(std::string::npos, w.find("8:warn_msg"));
    // warning() does NOT return an HTTP response; no status line
    EXPECT_EQ(std::string::npos, w.find("HTTP/"));
}

// TC-RS-04: gzip enabled — Content-Encoding: gzip; body decompresses to original
TEST(Response, gzip_response) {
    auto opts = make_opts(true /*gzip*/);
    std::string body_in = "hello gzip world";
    std::string resp = response(body_in, opts);
    std::string hdr  = header_of(resp);
    EXPECT_NE(std::string::npos, hdr.find("Content-Encoding: gzip"));

    // Decompress
    std::string compressed = body_of(resp);
    std::istringstream ss(compressed);
    boost::iostreams::filtering_streambuf<boost::iostreams::input> in;
    in.push(boost::iostreams::gzip_decompressor());
    in.push(ss);
    std::ostringstream out;
    boost::iostreams::copy(in, out);
    EXPECT_EQ(body_in, out.str());
}

// TC-RS-05: keep-alive reflected in header
TEST(Response, connection_close_header) {
    auto opts = make_opts(false, false, true /*http_close*/);
    std::string hdr = header_of(response("x", opts));
    EXPECT_NE(std::string::npos, hdr.find("Connection: Close"));
}

TEST(Response, no_connection_header_when_keepalive) {
    auto opts = make_opts(false, false, false);
    std::string hdr = header_of(response("x", opts));
    EXPECT_EQ(std::string::npos, hdr.find("Connection:"));
}

// TC-RS-06: empty body — Content-Length: 0, no crash
TEST(Response, empty_body) {
    auto opts = make_opts();
    std::string resp = response("", opts);
    EXPECT_NE(std::string::npos, header_of(resp).find("Content-Length: 0"));
    EXPECT_EQ("", body_of(resp));
}

// TC-RS-07: large body (1 MB) — Content-Length correct, no truncation
TEST(Response, large_body_correct_length) {
    auto opts = make_opts();
    std::string big(1 << 20, 'z'); // 1 MiB
    std::string resp = response(big, opts);
    std::string hdr  = header_of(resp);
    EXPECT_NE(std::string::npos, hdr.find("Content-Length: 1048576"));
    EXPECT_EQ(big, body_of(resp));
}
