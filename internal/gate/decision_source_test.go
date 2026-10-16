package gate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The record says where the class came from: the classifier when the gate classified the command itself, what the caller said
// (`pdx lease run` passes "classifier" or "kind"), and nothing when the person named the class.
func TestDecision_RecordsWhereTheClassCameFrom(t *testing.T) {
	g, _ := testGate(t)
	runSync(t, g, Options{Argv: sh("true"), Class: ClassHeavy})                      // named by the person
	runSync(t, g, Options{Argv: sh("true"), Class: ClassHeavy, ClassSource: "kind"}) // the alias fell back to the kind
	runSync(t, g, Options{Argv: sh("true"), Class: ClassLight, ClassSource: "classifier"})
	runSync(t, g, Options{Argv: []string{"go", "version"}, Real: "/usr/bin/true"}) // the gate classified it (not a command it holds)

	recs := readDecisions(t, g)
	require.Len(t, recs, 3, "go version is not held and leaves no line")
	assert.NotContains(t, recs[0], "class_source")
	assert.Equal(t, "kind", recs[1]["class_source"])
	assert.Equal(t, "classifier", recs[2]["class_source"])
}

func TestDecision_AClassTheGateWorkedOutIsFromTheClassifier(t *testing.T) {
	g, _ := testGate(t)
	runSync(t, g, Options{Argv: []string{"go", "test", "./..."}, Real: "/usr/bin/true"})
	recs := readDecisions(t, g)
	require.Len(t, recs, 1)
	assert.Equal(t, "heavy", recs[0]["class"])
	assert.Equal(t, "classifier", recs[0]["class_source"])
}
