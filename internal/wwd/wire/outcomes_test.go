package wire

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func goodDisagreement(edit ...func(*Disagreement)) Disagreement {
	d := Disagreement{ID: 3, Label: "task:purdex/go-changed", Host: "mserv", OtherHost: "air26", Tree: "7ee0", Task: "go-changed",
		Jobs: [2]string{"j_01A", "j_01B"}, At: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	for _, e := range edit {
		e(&d)
	}
	return d
}

func TestADisagreementIsCheckedBeforeAReaderActsOnIt(t *testing.T) {
	require.NoError(t, goodDisagreement().Validate())
	for name, edit := range map[string]func(*Disagreement){
		"no id":                                 func(d *Disagreement) { d.ID = 0 },
		"a label that is not a task label":      func(d *Disagreement) { d.Label = "os:linux" },
		"no host":                               func(d *Disagreement) { d.Host = "" },
		"the same host twice":                   func(d *Disagreement) { d.OtherHost = d.Host },
		"a control character in the other host": func(d *Disagreement) { d.OtherHost = "a\nb" },
		"no tree":                               func(d *Disagreement) { d.Tree = "" },
		"a tree too long":                       func(d *Disagreement) { d.Tree = strings.Repeat("a", 129) },
		"a job id that is not one":              func(d *Disagreement) { d.Jobs[0] = "../x" },
		"the same job twice":                    func(d *Disagreement) { d.Jobs[1] = d.Jobs[0] },
		"no time":                               func(d *Disagreement) { d.At = time.Time{} },
	} {
		assert.Error(t, goodDisagreement(edit).Validate(), name)
	}
}

func TestADisagreementListIsCheckedAsAWhole(t *testing.T) {
	a, b := goodDisagreement(), goodDisagreement(func(d *Disagreement) { d.ID = 7 })
	list := func(edit ...func(*DisagreementList)) DisagreementList {
		l := DisagreementList{Host: "mserv", Cursor: 7, Items: []Disagreement{a, b}, Origin: "mlab"}
		for _, e := range edit {
			e(&l)
		}
		return l
	}
	require.NoError(t, list().Validate())
	require.NoError(t, DisagreementList{Host: "mserv", Cursor: 12, Items: []Disagreement{}, Origin: "mlab"}.Validate(), "an empty list keeps the cursor asked with")
	for name, l := range map[string]DisagreementList{
		"no host":   list(func(l *DisagreementList) { l.Host = "" }),
		"no origin": list(func(l *DisagreementList) { l.Origin = "" }),
		"an item about another host": list(func(l *DisagreementList) {
			l.Items = []Disagreement{a, goodDisagreement(func(d *Disagreement) { d.ID = 7; d.Host = "mini" })}
		}),
		"ids that do not go up":            list(func(l *DisagreementList) { l.Items = []Disagreement{b, a} }),
		"a cursor that is not the last id": list(func(l *DisagreementList) { l.Cursor = 9 }),
		"a negative cursor":                list(func(l *DisagreementList) { l.Cursor = -1; l.Items = nil }),
		"an item that is not valid": list(func(l *DisagreementList) {
			l.Items = []Disagreement{a, goodDisagreement(func(d *Disagreement) { d.ID = 7; d.Label = "x" })}
		}),
	} {
		assert.Error(t, l.Validate(), name)
	}
	over := make([]Disagreement, MaxDisagreements+1)
	for i := range over {
		over[i] = goodDisagreement(func(d *Disagreement) { d.ID = int64(i + 1) })
	}
	assert.Error(t, DisagreementList{Host: "mserv", Cursor: int64(len(over)), Items: over, Origin: "mlab"}.Validate(), "more than the maximum")
}

// The body is decoded strictly, like every wire payload.
func TestADisagreementListIsDecodedStrictly(t *testing.T) {
	_, err := Decode[DisagreementList]([]byte(`{"host":"mserv","cursor":0,"items":[],"origin":"mlab","more":false,"extra":1}`))
	assert.Error(t, err)
	l, err := Decode[DisagreementList]([]byte(`{"host":"mserv","cursor":0,"items":[],"origin":"mlab","more":false}`))
	require.NoError(t, err)
	assert.NoError(t, l.Validate())
}
