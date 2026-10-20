package wire

import (
	"testing"
	"time"
)

// S3-2 (watch-rules plan): the level `watch` and the kinds pressure_watch, pressure_danger and swap_growth are valid on the wire. A
// reader that does not know them refuses the whole list (DecodeLenient still validates), which is why the rules that write them are
// behind [advisor] watch_rules and come after every reader has this build.
func TestAlertWatchKindsAndLevelAreValid(t *testing.T) {
	at := time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC)
	for _, k := range []AlertKind{AlertPressureWatch, AlertPressureDanger, AlertSwapGrowth} {
		if !k.Valid() {
			t.Errorf("kind %q is not valid", k)
		}
		if err := (Alert{Key: string(k), Kind: k, Level: AlertWatch, Since: at, Last: at}).Validate(); err != nil {
			t.Errorf("%s at level watch: %v", k, err)
		}
	}
	if string(AlertPressureWatch) != "pressure_watch" || string(AlertPressureDanger) != "pressure_danger" || string(AlertSwapGrowth) != "swap_growth" || string(AlertWatch) != "watch" {
		t.Fatalf("the wire spellings changed: %q %q %q %q", AlertPressureWatch, AlertPressureDanger, AlertSwapGrowth, AlertWatch)
	}
}

// What is not in the table is still refused: a typo must not pass for a new kind or level.
func TestAlertUnknownKindAndLevelAreRefused(t *testing.T) {
	at := time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC)
	if AlertKind("pressure_watching").Valid() || AlertLevel("watcher").Valid() || AlertLevel("").Valid() {
		t.Fatal("an unknown kind or level was accepted")
	}
	if err := (Alert{Key: "k", Kind: AlertPressureWatch, Level: AlertLevel("page"), Since: at, Last: at}).Validate(); err == nil {
		t.Error("an unknown level validated")
	}
}
