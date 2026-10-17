package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/gate"
)

func TestUsableHosts_KeepsTheRecordsOfTheHostsTheDaemonCallsUsable(t *testing.T) {
	f := gate.AssistFile{Hosts: map[string]gate.AssistHost{
		"air26": {SSH: "air26-lan", Can: []string{"go test"}},
		"mserv": {Can: []string{"phpunit"}},
		"old":   {},
	}}
	body := []byte(`{"repo":"purdex","hosts":[{"host":"air26","usable":true},{"host":"mserv","usable":false,"why":"not allowed"},{"host":"gone","usable":true}]}`)
	got := usableHosts(body, f)
	require.Len(t, got, 1, "mserv is not usable, gone has no record")
	assert.Equal(t, "air26-lan", got["air26"].SSH)
	assert.Equal(t, []string{"go test"}, got["air26"].Can)
	assert.Nil(t, usableHosts([]byte(`not json`), f))
}

// With no assist host asked on (every host but mlab) there is no walk at all: the gate never looks.
func TestGateAssistWalk_NoHostOnNoWalk(t *testing.T) {
	dir := t.TempDir()
	assert.Nil(t, gateAssistWalk("", dir), "no assist.json")
	_, err := gate.EditAssist(dir, func(f *gate.AssistFile) (bool, string, error) {
		f.Hosts = map[string]gate.AssistHost{"air26": {SSH: "air26"}}
		return true, "added", nil
	})
	require.NoError(t, err)
	assert.Nil(t, gateAssistWalk("", dir), "a host that is off")
	_, err = gate.EditAssist(dir, func(f *gate.AssistFile) (bool, string, error) {
		h := f.Hosts["air26"]
		h.On = true
		f.Hosts["air26"] = h
		f.Reserved = &gate.AssistReserved{By: "t", Reason: "release", At: time.Now(), Until: time.Now().Add(time.Hour)}
		return true, "on", nil
	})
	require.NoError(t, err)
	w := gateAssistWalk("", dir)
	require.NotNil(t, w)
	assert.True(t, w.Reserved(time.Now()), "the reservation is read at the look")
	assert.False(t, w.Reserved(time.Now().Add(2*time.Hour)))
}
