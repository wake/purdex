package profiles

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/profilehash"
)

// The guard is linear in the payload: tens of thousands of existing and new ids are checked in well under a second (a
// per-id scan of the stored order made this quadratic).
func TestDeviceAppendGuard_StaysLinearWithManyTabs(t *testing.T) {
	const old, added = 30000, 30000
	var oldOrder, oldTabs, order, tabs []string
	for i := 0; i < old; i++ {
		id := fmt.Sprintf("o%d", i)
		oldOrder = append(oldOrder, `"`+id+`"`)
		oldTabs = append(oldTabs, `"`+id+`":{}`)
	}
	order = append(order, oldOrder...)
	tabs = append(tabs, oldTabs...)
	for i := 0; i < added; i++ {
		id := fmt.Sprintf("n%d", i)
		order = append(order, `"`+id+`"`)
		tabs = append(tabs, fmt.Sprintf(`"%s":{"id":"%s","pinned":false,"locked":false,"createdAt":1,"layout":{}}`, id, id))
	}
	build := func(o, tb []string) string {
		return `{"order":[` + strings.Join(o, ",") + `],"tabs":{` + strings.Join(tb, ",") + `}}`
	}
	stored, next := build(oldOrder, oldTabs), build(order, tabs)
	h, err := profilehash.Sum([]byte(next))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = deviceAppendGuard(Section{Payload: []byte(stored), Fingerprint: "f", Ordinal: 1}, Section{Payload: []byte(next), Hash: h, Fingerprint: "f", Ordinal: 1})
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("the guard took %v for %d existing and %d new tabs", d, old, added)
	}
}
