package agent

import (
	"testing"
	"time"
)

func TestExtractHookAgentVersion(t *testing.T) {
	cases := map[string]string{
		"codex-cli 0.121.0":     "0.121.0",
		"2.1.114 (Claude Code)": "2.1.114",
		"v1.2.3-beta.1 release": "1.2.3",
		"no version here":       "",
	}

	for input, want := range cases {
		if got := ExtractHookAgentVersion(input); got != want {
			t.Fatalf("ExtractHookAgentVersion(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCompareHookAgentVersions(t *testing.T) {
	cases := []struct {
		a    string
		b    string
		want int
	}{
		{a: "0.121.0", b: "0.121.0", want: 0},
		{a: "0.122.0", b: "0.121.0", want: 1},
		{a: "0.121.0", b: "0.122.0", want: -1},
		{a: "2.1.114", b: "2.1.99", want: 1},
		{a: "2.1", b: "2.1.0", want: 0},
		{a: "", b: "2.1.0", want: -1},
	}

	for _, tc := range cases {
		if got := CompareHookAgentVersions(tc.a, tc.b); got != tc.want {
			t.Fatalf("CompareHookAgentVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestDetectHookAgentVersion_Cached confirms the per-binary TTL cache so
// CheckHooks does not pay a subprocess cost on every call.
func TestDetectHookAgentVersion_Cached(t *testing.T) {
	ResetHookAgentVersionCache()
	t.Cleanup(ResetHookAgentVersionCache)

	// Seed the cache by hitting a binary that definitely does not exist;
	// the cached value is "" and that is fine — we are verifying the cache,
	// not a real CLI.
	const missing = "pdx-nonexistent-binary-for-test"
	if v := DetectHookAgentVersion(missing, "--version"); v != "" {
		t.Fatalf("expected empty version for missing binary, got %q", v)
	}

	// Force TTL to expire only when we decide — drop window to 1h so the
	// second call definitely hits the cache.
	prev := hookVersionTTL
	hookVersionTTL = time.Hour
	t.Cleanup(func() { hookVersionTTL = prev })

	hookVersionMu.Lock()
	c, ok := hookVersionCache[missing]
	hookVersionMu.Unlock()
	if !ok {
		t.Fatal("expected cache entry after first detection")
	}
	if c.value != "" {
		t.Fatalf("unexpected cached value: %q", c.value)
	}
	seededAt := c.at

	// Subsequent calls within TTL must return the cached value without
	// refreshing the timestamp. Sleep briefly so a refresh would be observable.
	time.Sleep(5 * time.Millisecond)
	for i := 0; i < 3; i++ {
		if v := DetectHookAgentVersion(missing, "--version"); v != "" {
			t.Fatalf("call %d: expected empty cached version, got %q", i, v)
		}
	}

	hookVersionMu.Lock()
	refreshed := hookVersionCache[missing]
	hookVersionMu.Unlock()
	if !refreshed.at.Equal(seededAt) {
		t.Fatal("cache entry timestamp changed within TTL — cache was bypassed")
	}
}
