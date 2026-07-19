package execution

import "testing"

func TestStatus_IsTerminal(t *testing.T) {
	cases := map[Status]bool{
		StatusAccepted:  false,
		StatusRunning:   false,
		StatusCompleted: true,
		StatusFailed:    true,
	}
	for s, want := range cases {
		if got := s.IsTerminal(); got != want {
			t.Errorf("Status(%q).IsTerminal() = %v, want %v", s, got, want)
		}
	}
}

func TestValidStatus(t *testing.T) {
	for _, s := range []Status{StatusAccepted, StatusRunning, StatusCompleted, StatusFailed} {
		if !ValidStatus(s) {
			t.Errorf("ValidStatus(%q) = false, want true", s)
		}
	}
	for _, s := range []Status{"", "pending", "cancelled", "ACCEPTED"} {
		if ValidStatus(s) {
			t.Errorf("ValidStatus(%q) = true, want false", s)
		}
	}
}

func TestCanTransition_Legal(t *testing.T) {
	legal := []struct{ from, to Status }{
		{StatusAccepted, StatusRunning},
		{StatusAccepted, StatusFailed},
		{StatusRunning, StatusCompleted},
		{StatusRunning, StatusFailed},
	}
	for _, tc := range legal {
		if !CanTransition(tc.from, tc.to) {
			t.Errorf("CanTransition(%q, %q) = false, want true", tc.from, tc.to)
		}
	}
}

func TestCanTransition_Illegal(t *testing.T) {
	illegal := []struct{ from, to Status }{
		// terminal states cannot leave
		{StatusCompleted, StatusRunning},
		{StatusCompleted, StatusFailed},
		{StatusFailed, StatusRunning},
		{StatusFailed, StatusCompleted},
		// no skipping accepted -> completed
		{StatusAccepted, StatusCompleted},
		// no backward
		{StatusRunning, StatusAccepted},
		// self transitions are not advances
		{StatusAccepted, StatusAccepted},
		{StatusRunning, StatusRunning},
		// unknown states
		{"bogus", StatusRunning},
		{StatusAccepted, "bogus"},
	}
	for _, tc := range illegal {
		if CanTransition(tc.from, tc.to) {
			t.Errorf("CanTransition(%q, %q) = true, want false", tc.from, tc.to)
		}
	}
}

func TestValidLaunchState(t *testing.T) {
	for _, ls := range []LaunchState{LaunchNone, LaunchLaunching, LaunchLaunched} {
		if !ValidLaunchState(ls) {
			t.Errorf("ValidLaunchState(%q) = false, want true", ls)
		}
	}
	for _, ls := range []LaunchState{"", "spawned", "NONE"} {
		if ValidLaunchState(ls) {
			t.Errorf("ValidLaunchState(%q) = true, want false", ls)
		}
	}
}
