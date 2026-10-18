package manifest

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Shape is a task's command as the entry sees it: the literal words, where the caller's arguments go, and the words that only
// carry an output file of the worker's (which a caller never types).
func TestShape_WordsOfATask(t *testing.T) {
	m, err := Parse([]byte(`
[tasks.spa-file]
run  = "pnpm vitest run --reporter=json --outputFile={report} {files}"
cwd  = "spa"
args = "files"
[tasks.lint]
run  = "make lint"
args = "none"
[tasks.go-pkg]
run  = "go test -json -junitfile {junit} {packages}"
args = "packages"
`))
	require.NoError(t, err)
	assert.Equal(t, []ShapeWord{
		{Kind: ShapeLiteral, Text: "pnpm"}, {Kind: ShapeLiteral, Text: "vitest"}, {Kind: ShapeLiteral, Text: "run"},
		{Kind: ShapeLiteral, Text: "--reporter=json"}, {Kind: ShapeOutput, Text: "--outputFile="}, {Kind: ShapeArgs},
	}, m.Tasks["spa-file"].Shape())
	assert.Equal(t, []ShapeWord{{Kind: ShapeLiteral, Text: "make"}, {Kind: ShapeLiteral, Text: "lint"}}, m.Tasks["lint"].Shape())
	assert.Equal(t, []ShapeWord{
		{Kind: ShapeLiteral, Text: "go"}, {Kind: ShapeLiteral, Text: "test"}, {Kind: ShapeLiteral, Text: "-json"},
		{Kind: ShapeLiteral, Text: "-junitfile"}, {Kind: ShapeOutput}, {Kind: ShapeArgs},
	}, m.Tasks["go-pkg"].Shape())
}

// A copy: changing the shape does not change the task.
func TestShape_IsACopy(t *testing.T) {
	m, err := Parse([]byte("[tasks.lint]\nrun = \"make lint\"\nargs = \"none\"\n"))
	require.NoError(t, err)
	s := m.Tasks["lint"].Shape()
	s[0].Text = "changed"
	assert.Equal(t, "make", m.Tasks["lint"].Shape()[0].Text)
}
