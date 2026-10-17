package daemon

import (
	"strings"
	"testing"
)

func TestConfigChecksTheOwnerIdleMinutes(t *testing.T) {
	for _, bad := range []string{"-1", "10000"} {
		if _, err := load(t, "[owner]\nenabled = true\nidle_minutes = "+bad+"\n"); err == nil || !strings.Contains(err.Error(), "idle_minutes") {
			t.Errorf("idle_minutes = %s accepted (err %v)", bad, err)
		}
	}
	for _, ok := range []string{"0", "1", "5", "9999"} {
		if _, err := load(t, "[owner]\nenabled = true\nidle_minutes = "+ok+"\n"); err != nil {
			t.Errorf("idle_minutes = %s: %v", ok, err)
		}
	}
}

func TestConfigRejectsNegativeSlots(t *testing.T) {
	for field, body := range map[string]string{
		"heavy": "[worker]\nheavy = -1\n",
		"light": "[worker]\nlight = -2\n",
	} {
		if _, err := load(t, body); err == nil || !strings.Contains(err.Error(), field) {
			t.Errorf("%s: accepted (err %v)", field, err)
		}
	}
	if _, err := load(t, "[worker]\nheavy = 0\nlight = 0\n"); err != nil {
		t.Errorf("explicit zeros are valid: %v", err)
	}
}
