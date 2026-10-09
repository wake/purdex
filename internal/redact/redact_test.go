package redact

import (
	"strings"
	"testing"
)

func TestString_Redacts(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"bearer":        {"curl -H 'Authorization: Bearer abc.DEF-123_x' url", "curl -H 'Authorization: [redacted]' url"},
		"bearer case":   {"authorization: bearer sometoken", "authorization: [redacted]"},
		"sk":            {"key sk-ant-api03-AbCdEf123456 here", "key [redacted] here"},
		"github":        {"token ghp_AbCdEf1234567890 and gho_ZyXwVu9876543210", "token [redacted] and [redacted]"},
		"slack":         {"hook xoxb-1234-abcd-EFGH ok", "hook [redacted] ok"},
		"aws":           {"id AKIAIOSFODNN7EXAMPLE end", "id [redacted] end"},
		"pdxp":          {"pdxp_AbCdEf1234567890xyz", "[redacted]"},
		"pdxd":          {"auth pdxd_Zz99-Yy88_Xx77", "auth [redacted]"},
		"mixed 32":      {"x " + strings.Repeat("aB3", 11) + " y", "x [redacted] y"}, // 33 chars, letters and digits
		"sha 40 hex":    {"commit 6b9ba94afb8a0f1c3d2e4a5b6c7d8e9f01234567 done", "commit [redacted] done"},
		"exactly 32":    {strings.Repeat("a1", 16), "[redacted]"},
		"two in a line": {"sk-abcdefgh12345 and ghp_abcdefgh12345", "[redacted] and [redacted]"},
	}
	for name, c := range cases {
		if got := String(c.in); got != c.want {
			t.Errorf("%s: String(%q) = %q, want %q", name, c.in, got, c.want)
		}
	}
}

func TestString_LeavesOrdinaryText(t *testing.T) {
	for name, in := range map[string]string{
		"words":          "做完了。接著測試 the quick brown fox",
		"path":           "/Users/wake/Workspace/wake/purdex/internal/module/workbook/store.go",
		"31 mixed":       strings.Repeat("aB3", 10) + "x",
		"pure digits":    strings.Repeat("1", 40),
		"pure letters":   strings.Repeat("a", 40),
		"bearer word":    "the bearer of bad news",
		"short sk":       "sk-1 and risk-free",
		"hyphen words":   "a-very-long-hyphenated-word-that-goes-on-and-on-0123456789",
		"empty":          "",
		"already marked": "[redacted]",
	} {
		if got := String(in); got != in {
			t.Errorf("%s: String(%q) = %q, want it unchanged", name, in, got)
		}
	}
}

func TestString_Idempotent(t *testing.T) {
	in := "Bearer abc sk-abcdefgh12345 " + strings.Repeat("aB3", 12) + " pdxp_abc123def456"
	once := String(in)
	if twice := String(once); twice != once {
		t.Fatalf("not idempotent: %q then %q", once, twice)
	}
	if strings.Contains(once, "abc123def456") || strings.Contains(once, "sk-abcdefgh") {
		t.Fatalf("left a secret: %q", once)
	}
}
