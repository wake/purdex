package config_test

import (
	"bytes"
	"testing"

	"github.com/BurntSushi/toml"
)

// BG-4b (badge spec §7.2): [push] read_flush_minutes is how long a phone with pending reads may go without any push before
// the daemon sends one just for them. 15 when unset; 1–120 otherwise; anything else is refused (the default applies and the
// caller is told it was not valid).
func TestPush_ReadFlushMinutes(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       int
		valid      bool
	}{
		{"no section", "port = 9000\n", 15, true},
		{"unset", "[push]\napns_dir = \"/a\"\n", 15, true},
		{"the lowest", "[push]\nread_flush_minutes = 1\n", 1, true},
		{"the highest", "[push]\nread_flush_minutes = 120\n", 120, true},
		{"in between", "[push]\nread_flush_minutes = 30\n", 30, true},
		{"zero is unset", "[push]\nread_flush_minutes = 0\n", 15, true},
		{"below the range", "[push]\nread_flush_minutes = -3\n", 15, false},
		{"above the range", "[push]\nread_flush_minutes = 121\n", 15, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, valid := loadToml(t, tc.body).PushReadFlushMinutes()
			if got != tc.want || valid != tc.valid {
				t.Fatalf("PushReadFlushMinutes() = %d, %v; want %d, %v", got, valid, tc.want, tc.valid)
			}
		})
	}
}

func TestPush_ReadFlushMinutesSurvivesCloneAndEncode(t *testing.T) {
	a := loadToml(t, "[push]\napns_dir = \"/a\"\nread_flush_minutes = 7\n")
	b := a.Clone()
	b.Push.ReadFlushMinutes = 99
	if got, _ := a.PushReadFlushMinutes(); got != 7 {
		t.Fatalf("the clone shares the original: %d", got)
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(a); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("read_flush_minutes = 7")) {
		t.Fatalf("not written back:\n%s", buf.String())
	}
	var unset bytes.Buffer
	if err := toml.NewEncoder(&unset).Encode(loadToml(t, "[push]\napns_dir = \"/a\"\n")); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(unset.Bytes(), []byte("read_flush_minutes")) {
		t.Fatalf("an unset read_flush_minutes was written back:\n%s", unset.String())
	}
}
