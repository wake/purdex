package session

import (
	"testing"
	"time"
)

// BG-4: the reads module checks a session code against the list the daemon already holds in memory — never a tmux fork of
// its own (the list is refilled by the clients' GET /api/sessions). Stale, invalidated or never filled means "unknown".
func TestCachedSessionCodes(t *testing.T) {
	m := &SessionModule{}
	if _, ok := m.CachedSessionCodes(time.Minute); ok {
		t.Fatal("a list that was never filled must be unknown")
	}
	m.listCacheMu.Lock()
	m.listCacheData = []SessionInfo{{Code: "c1"}, {Code: "c2"}}
	m.listCacheAt = time.Now()
	m.listCacheMu.Unlock()
	codes, ok := m.CachedSessionCodes(time.Minute)
	if !ok || len(codes) != 2 {
		t.Fatalf("codes = %v ok=%v", codes, ok)
	}
	if _, has := codes["c1"]; !has {
		t.Fatal("c1 missing")
	}
	m.invalidateListCache() // a session was created / renamed / deleted since: the list is no evidence
	if _, ok := m.CachedSessionCodes(time.Minute); ok {
		t.Fatal("an invalidated list must be unknown")
	}
	m.listCacheMu.Lock()
	m.listCacheAt = time.Now().Add(-2 * time.Minute)
	m.listCacheMu.Unlock()
	if _, ok := m.CachedSessionCodes(time.Minute); ok {
		t.Fatal("an old list must be unknown")
	}
}

// An empty list that was really read is evidence ("no sessions"), unlike no list at all.
func TestCachedSessionCodes_AnEmptyReadListIsKnown(t *testing.T) {
	m := &SessionModule{}
	m.listCacheMu.Lock()
	m.listCacheData = []SessionInfo{}
	m.listCacheAt = time.Now()
	m.listCacheMu.Unlock()
	if codes, ok := m.CachedSessionCodes(time.Minute); !ok || len(codes) != 0 {
		t.Fatalf("codes = %v ok=%v", codes, ok)
	}
}
