package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGate_SetProfileAndStatusShowTheSourceOfEverySetting(t *testing.T) {
	cfg, _ := gateEnv(t, "[admission]\ndisk_min_gb = 0\n")
	var out, errb bytes.Buffer
	assert.Equal(t, 0, gateMain([]string{"set", "--config", cfg, "admission.disk_min_gb=0"}, &out, &errb), errb.String())
	assert.Equal(t, 0, gateMain([]string{"set", "--config", cfg, "slots.heavy=2"}, &out, &errb), errb.String())
	assert.Contains(t, out.String(), "slots.heavy")
	out.Reset()
	assert.Equal(t, 0, gateMain([]string{"set", "--config", cfg, "--profile", "night", "slots.heavy=3"}, &out, &errb), errb.String())
	assert.Equal(t, 0, gateMain([]string{"profile", "use", "--config", cfg, "night"}, &out, &errb), errb.String())
	out.Reset()
	assert.Equal(t, 0, gateMain([]string{"profile", "list", "--config", cfg}, &out, &errb))
	assert.Contains(t, out.String(), "* night")
	out.Reset()
	assert.Equal(t, 0, gateMain([]string{"status", "--config", cfg}, &out, &errb))
	assert.Regexp(t, `slots\.heavy\s+3\s+profile`, out.String())
	assert.Regexp(t, `slots\.light\s+3\s+default`, out.String())
	assert.Regexp(t, `admission\.disk_min_gb\s+0\s+config`, out.String())

	errb.Reset()
	assert.Equal(t, 1, gateMain([]string{"set", "--config", cfg, "slots.heavy=0"}, &out, &errb))
	assert.Contains(t, errb.String(), "slots.heavy")
	assert.Equal(t, 2, gateMain([]string{"set", "--config", cfg, "nonsense"}, &out, &errb))
}
