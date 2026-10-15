package gate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A comment with an odd number of triple quotes is a comment, not the start of a string.
func TestSet_ATripleQuoteInACommentDoesNotHideTheTable(t *testing.T) {
	p := cfgFile(t, "# example: \"\"\"\n[gate.slots]\nheavy = 1 # was '''\nlight = 3\n")
	_, err := Set(p, "", "slots.heavy", "2")
	require.NoError(t, err)
	assert.Equal(t, "# example: \"\"\"\n[gate.slots]\nheavy = 2\nlight = 3\n", read(t, p))
}

func TestBeforeComment(t *testing.T) {
	assert.Equal(t, "a = 1 ", beforeComment("a = 1 # c"))
	assert.Equal(t, `a = "x # y" `, beforeComment(`a = "x # y" # z`))
	assert.Equal(t, `a = 'x # y' `, beforeComment(`a = 'x # y' # z`))
	assert.Equal(t, `a = "q\"#" `, beforeComment(`a = "q\"#" # z`))
}
