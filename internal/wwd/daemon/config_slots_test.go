package daemon

import (
	"strings"
	"testing"
)

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
