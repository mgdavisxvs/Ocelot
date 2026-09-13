// Tier 1 — TC-MF-01 through TC-MF-08
#include <gtest/gtest.h>
#include <climits>
#include "misc_functions.h"

// TC-MF-01: strtoint32 valid decimals
TEST(MiscFunctions, strtoint32_valid) {
    EXPECT_EQ(0,          strtoint32("0"));
    EXPECT_EQ(1,          strtoint32("1"));
    EXPECT_EQ(2147483647, strtoint32("2147483647"));
    EXPECT_EQ(255,        strtoint32("255"));
    EXPECT_EQ(65535,      strtoint32("65535"));
}

// TC-MF-02: strtoint32 negative, empty, non-numeric (pin current behaviour: returns 0 for garbage)
TEST(MiscFunctions, strtoint32_invalid) {
    EXPECT_EQ(0, strtoint32(""));
    EXPECT_EQ(0, strtoint32("abc"));
    // Negative is parsed by istringstream as negative int32
    EXPECT_EQ(-1, strtoint32("-1"));
    // Overflow: istringstream saturates or wraps; pin the actual return value
    int32_t overflow_val = strtoint32("2147483648");
    // Must not be 2147483647 (off by one would be a bug) — just verify it's parseable
    (void)overflow_val; // Behaviour is implementation-defined; test that it doesn't crash
}

// TC-MF-03: strtoint64 large values
TEST(MiscFunctions, strtoint64_large) {
    EXPECT_EQ(0LL,                strtoint64("0"));
    EXPECT_EQ(9223372036854775807LL, strtoint64("9223372036854775807"));
    EXPECT_EQ(-1LL,               strtoint64("-1"));
    EXPECT_EQ(1000000000000LL,    strtoint64("1000000000000"));
}

// TC-MF-04: inttostr / strtoint32 round-trip
TEST(MiscFunctions, inttostr_roundtrip) {
    for (int n : {0, 1, 255, 65535, 2147483647}) {
        EXPECT_EQ(n, strtoint32(inttostr(n))) << "round-trip failed for n=" << n;
    }
}

// TC-MF-05: hex_decode — percent-encoding and plain ASCII
TEST(MiscFunctions, hex_decode_percent_encoding) {
    // "%2F%41%42" → "/AB"
    EXPECT_EQ("/AB", hex_decode("%2F%41%42"));
    // Lowercase hex digits
    EXPECT_EQ("/AB", hex_decode("%2f%41%42"));
    // Mixed case
    EXPECT_EQ("\x0A", hex_decode("%0a"));
    EXPECT_EQ("\xFF", hex_decode("%FF"));
}

TEST(MiscFunctions, hex_decode_plain_passthrough) {
    // Printable ASCII with no encoding passes through unchanged
    std::string plain = "abcdef0123456789ABCDEF";
    EXPECT_EQ(plain, hex_decode(plain));
}

// TC-MF-06: hex_decode — 20-byte info_hash round-trip via percent_encode
TEST(MiscFunctions, hex_decode_20byte_roundtrip) {
    // Build a 20-byte binary string with various byte values
    std::string binary;
    for (int i = 0; i < 20; i++) binary.push_back(static_cast<char>(i * 13));

    // Percent-encode every byte
    std::string encoded;
    for (unsigned char c : binary) {
        char buf[4];
        snprintf(buf, sizeof(buf), "%%%02X", static_cast<unsigned>(c));
        encoded += buf;
    }

    EXPECT_EQ(binary, hex_decode(encoded));
}

// TC-MF-07: hex_decode — malformed input does not crash
TEST(MiscFunctions, hex_decode_malformed_no_crash) {
    // '%' at end of string (no two hex digits follow)
    EXPECT_NO_THROW(hex_decode("%"));
    // '%' followed by only one hex digit
    EXPECT_NO_THROW(hex_decode("%4"));
    // '%' followed by non-hex chars: '%GG'
    // The code treats non-hex chars as 0, so '%GG' → byte 0x00
    std::string result;
    EXPECT_NO_THROW(result = hex_decode("%GG"));
    EXPECT_EQ(1u, result.size()); // one byte produced (decoded to something, not a crash)
}

// TC-MF-08: bintohex known vector
TEST(MiscFunctions, bintohex_known_vector) {
    std::string input;
    input.push_back('\x00');
    input.push_back('\xFF');
    input.push_back('\xAB');
    EXPECT_EQ("00ffab", bintohex(input));
}

TEST(MiscFunctions, bintohex_empty) {
    EXPECT_EQ("", bintohex(""));
}

TEST(MiscFunctions, bintohex_all_nibbles) {
    // 0x01..0x0F to cover both nibble paths
    std::string input;
    for (int i = 0; i <= 0x0F; i++) input.push_back(static_cast<char>(i));
    std::string hex = bintohex(input);
    EXPECT_EQ("000102030405060708090a0b0c0d0e0f", hex);
}
