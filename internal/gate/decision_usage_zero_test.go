package gate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A command that started and used nothing worth a number still says so: the fields are there, not absent like an older record's.
func TestDecision_AZeroUsageIsStillWritten(t *testing.T) {
	g, _ := testGate(t)
	runSync(t, g, Options{Argv: sh("true"), Class: ClassHeavy})
	rec := readDecisions(t, g)[0]
	for _, k := range []string{"cpu_s", "max_rss_mb", "major_faults"} {
		assert.Contains(t, rec, k)
	}
}
