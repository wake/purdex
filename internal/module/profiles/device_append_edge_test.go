package profiles

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wake/purdex/internal/devices"
)

func phonePrincipal(profileID string) *devices.Principal {
	return &devices.Principal{ID: devID, PairingID: "pair", ProfileID: profileID}
}

const goodTail = `"pinned":false,"locked":false,"createdAt":1,"layout":{}`

// Single-field omissions of a new tab, each refused on its own.
func TestDeviceAppend_ANewTabMissingOneRequiredField(t *testing.T) {
	prefix := `{"order":["t1","p1"],"tabs":{"t1":{"id":"t1","pinned":true,"createdAt":1789999999999},"p1":`
	suffix := `},"extra":{"a":1}}`
	for name, tab := range map[string]string{
		"no id":         `{"pinned":false,"locked":false,"createdAt":1,"layout":{}}`,
		"id not string": `{"id":7,` + goodTail + `}`,
		"no locked":     `{"id":"p1","pinned":false,"createdAt":1,"layout":{}}`,
		"locked string": `{"id":"p1","pinned":false,"locked":"x","createdAt":1,"layout":{}}`,
		"no pinned":     `{"id":"p1","locked":false,"createdAt":1,"layout":{}}`,
		"no createdAt":  `{"id":"p1","pinned":false,"locked":false,"layout":{}}`,
		"no layout":     `{"id":"p1","pinned":false,"locked":false,"createdAt":1}`,
	} {
		e := newAppendEnv(t)
		rr := e.put(t, "tabs.w1", devClient, prefix+tab+suffix, "", nil)
		assert.Equal(t, http.StatusForbidden, rr.Code, name)
	}
}

// An empty new id with no id field must not pass the id==key check by comparing "" with "".
func TestDeviceAppend_AnEmptyIDNeedsAnIDField(t *testing.T) {
	e := newAppendEnv(t)
	p := `{"order":["t1",""],"tabs":{"t1":{"id":"t1","pinned":true,"createdAt":1789999999999},"":{` + goodTail + `}},"extra":{"a":1}}`
	assert.Equal(t, http.StatusForbidden, e.put(t, "tabs.w1", devClient, p, "", nil).Code)
}

// A stored order may name an id that has no tab entry (the admin wrote it); a device may not add that id again.
func TestDeviceAppend_ANewIDMayNotReuseAnIDOnlyInTheStoredOrder(t *testing.T) {
	m, _ := newTestModule(t)
	mine := createProfile(t, m, "mine")
	stored := `{"order":["t1","ghost"],"tabs":{"t1":{"id":"t1"}}}`
	rev := putSection(t, m, mine, "tabs.w1", "c_aaaaaaaaaaaa", 0, sumOf(t, stored), stored)
	e := &appendEnv{m: m, mine: mine, rev: rev, phone: phonePrincipal(mine)}
	e.rec = &eventRecorder{}
	p := `{"order":["t1","ghost","new"],"tabs":{"t1":{"id":"t1"},"new":{"id":"new",` + goodTail + `}}}`
	assert.Equal(t, http.StatusOK, e.put(t, "tabs.w1", devClient, p, "", nil).Code, "a really new id is fine")
	e2 := &appendEnv{m: m, mine: mine, rev: rev + 1, phone: phonePrincipal(mine), rec: &eventRecorder{}}
	q := `{"order":["t1","ghost","new","ghost"],"tabs":{"t1":{"id":"t1"},"new":{"id":"new",` + goodTail + `},"ghost":{"id":"ghost",` + goodTail + `}}}`
	assert.Equal(t, http.StatusForbidden, e2.put(t, "tabs.w1", devClient, q, "", nil).Code)
}
