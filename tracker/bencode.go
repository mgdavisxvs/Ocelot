package tracker

import (
	"fmt"
	"sort"
	"strings"
)

// Bencode implements BitTorrent's bencoding format
// Spec: https://www.bittorrent.org/beps/bep_0003.html

// BencodeString encodes a string: <length>:<contents>
// Example: "spam" → "4:spam"
func BencodeString(s string) string {
	return fmt.Sprintf("%d:%s", len(s), s)
}

// BencodeInt encodes an integer: i<value>e
// Example: 42 → "i42e"
func BencodeInt(n int64) string {
	return fmt.Sprintf("i%de", n)
}

// BencodeDict encodes a dictionary: d<key><value>...e
// Keys must be sorted lexicographically
func BencodeDict(items map[string]string) string {
	if len(items) == 0 {
		return "de"
	}

	// Sort keys (bencode requirement)
	keys := make([]string, 0, len(items))
	for k := range items {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("d")
	for _, k := range keys {
		b.WriteString(BencodeString(k))
		b.WriteString(items[k])
	}
	b.WriteString("e")
	return b.String()
}

// BencodeList encodes a list: l<value>...e
func BencodeList(items []string) string {
	var b strings.Builder
	b.WriteString("l")
	for _, item := range items {
		b.WriteString(item)
	}
	b.WriteString("e")
	return b.String()
}
