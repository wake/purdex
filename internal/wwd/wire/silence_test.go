package wire

import "testing"

// SilenceKind tells the two silences about a peer apart by the words the writer used (SilenceDetailWwdDown, SilenceDetailHostDown); any
// other text, and any alert that is not a silence, is "unknown" or empty.
func TestSilenceKind(t *testing.T) {
	cases := []struct {
		name string
		a    Alert
		want string
	}{
		{"host down", Alert{Kind: AlertSilence, Detail: SilenceDetailHostDown("air19")}, SilenceHostDownKind},
		{"wwd down", Alert{Kind: AlertSilence, Detail: SilenceDetailWwdDown("air19")}, SilenceWwdDownKind},
		{"some other words", Alert{Kind: AlertSilence, Detail: "air19：不知道為什麼"}, SilenceUnknownKind},
		{"no detail", Alert{Kind: AlertSilence}, SilenceUnknownKind},
		{"not a silence", Alert{Kind: AlertDiskLow, Detail: SilenceDetailHostDown("air19")}, ""},
	}
	for _, c := range cases {
		if got := SilenceKind(c.a); got != c.want {
			t.Errorf("%s: SilenceKind = %q, want %q", c.name, got, c.want)
		}
	}
}
