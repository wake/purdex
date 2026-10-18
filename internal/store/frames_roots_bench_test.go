package store

import "testing"

func benchRootsStore(b *testing.B) *FramesStore {
	b.Helper()
	events, err := OpenAgentEvent(":memory:")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { events.Close() })
	s, err := events.Frames()
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 11; i++ { // the size of the table on the daemon that was profiled
		sid := "S" + string(rune('A'+i%3))
		if _, err := s.Upsert(Frame{PaneID: "%" + string(rune('1'+i)), AgentType: "cc", PID: 100 + i, ProcessStartTime: "t", SessionID: sid, StartedAt: 1, LastSeenAt: 1}); err != nil {
			b.Fatal(err)
		}
	}
	return s
}

func BenchmarkListRootsBySessionID(b *testing.B) {
	s := benchRootsStore(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.ListRootsBySessionID("SA"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListRootsBySessionIDCached(b *testing.B) {
	s := benchRootsStore(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.ListRootsBySessionIDCached("SA"); err != nil {
			b.Fatal(err)
		}
	}
}
