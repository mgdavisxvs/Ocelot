package tracker

import "testing"

func TestBencodeString(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"spam", "4:spam"},
		{"", "0:"},
		{"hello world", "11:hello world"},
		{"a", "1:a"},
	}
	for _, c := range cases {
		got := BencodeString(c.in)
		if got != c.want {
			t.Errorf("BencodeString(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBencodeInt(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "i0e"},
		{42, "i42e"},
		{-1, "i-1e"},
		{1<<31 - 1, "i2147483647e"},
		{-9999999, "i-9999999e"},
	}
	for _, c := range cases {
		got := BencodeInt(c.in)
		if got != c.want {
			t.Errorf("BencodeInt(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBencodeDict_Empty(t *testing.T) {
	got := BencodeDict(map[string]string{})
	if got != "de" {
		t.Errorf("empty dict = %q, want \"de\"", got)
	}
}

func TestBencodeDict_Sorted(t *testing.T) {
	// Keys must appear in lexicographic order per BEP-3.
	m := map[string]string{
		"z": BencodeInt(1),
		"a": BencodeInt(2),
		"m": BencodeInt(3),
	}
	got := BencodeDict(m)
	// Expect: d 1:a i2e 1:m i3e 1:z i1e e
	want := "d1:ai2e1:mi3e1:zi1ee"
	if got != want {
		t.Errorf("BencodeDict sorted = %q, want %q", got, want)
	}
}

func TestBencodeDict_TypicalAnnounce(t *testing.T) {
	peers := "AAAAAA" // 6 bytes of compact peer data
	m := map[string]string{
		"complete":     BencodeInt(5),
		"incomplete":   BencodeInt(3),
		"interval":     BencodeInt(1800),
		"min interval": BencodeInt(1800),
		"peers":        BencodeString(peers),
	}
	got := BencodeDict(m)
	// Spot-check: must start with 'd', end with 'e', contain all keys
	if got[0] != 'd' || got[len(got)-1] != 'e' {
		t.Errorf("BencodeDict structure wrong: %s", got)
	}
	for _, key := range []string{"complete", "incomplete", "interval", "peers"} {
		enc := BencodeString(key)
		if !containsStr(got, enc) {
			t.Errorf("BencodeDict missing key %q", key)
		}
	}
}

func TestBencodeList_Empty(t *testing.T) {
	got := BencodeList([]string{})
	if got != "le" {
		t.Errorf("empty list = %q, want \"le\"", got)
	}
}

func TestBencodeList_Elements(t *testing.T) {
	items := []string{BencodeInt(1), BencodeString("foo"), BencodeInt(99)}
	got := BencodeList(items)
	want := "li1e3:fooi99ee"
	if got != want {
		t.Errorf("BencodeList = %q, want %q", got, want)
	}
}

func TestBencodeString_BinaryContent(t *testing.T) {
	// info_hash is raw binary; BencodeString must not escape it.
	raw := "\x00\x01\x02\xff\xfe"
	got := BencodeString(raw)
	if got != "5:\x00\x01\x02\xff\xfe" {
		t.Errorf("BencodeString binary = %q", got)
	}
}

func containsStr(haystack, needle string) bool {
	return len(haystack) >= len(needle) &&
		func() bool {
			for i := 0; i <= len(haystack)-len(needle); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		}()
}
