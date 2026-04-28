package agent

import "testing"

func TestLifecycleEventKind_String_AllCases(t *testing.T) {
	kinds := []LifecycleEventKind{
		LifecycleNone,
		LifecycleSessionStart,
		LifecycleUserPromptSubmit,
		LifecycleStop,
		LifecycleStopFailure,
		LifecycleSessionEnd,
		LifecycleSubagentStart,
		LifecycleSubagentStop,
	}

	seen := make(map[string]LifecycleEventKind, len(kinds))
	for _, k := range kinds {
		s := k.String()
		if s == "" {
			t.Errorf("kind %d: String() returned empty", int(k))
			continue
		}
		if other, dup := seen[s]; dup {
			t.Errorf("kind %d and %d both stringify to %q", int(other), int(k), s)
			continue
		}
		seen[s] = k
	}

	if len(seen) != len(kinds) {
		t.Errorf("expected %d unique strings, got %d", len(kinds), len(seen))
	}

	if got := LifecycleEventKind(99).String(); got == "" {
		t.Errorf("out-of-range kind String() should return non-empty placeholder; got empty")
	}
}
