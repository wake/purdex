package gate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A header-looking line inside a multi-line string is text: the set lands in the real table, or is refused, and never
// changes the string.
func TestSet_AFakeHeaderInsideAStringIsNotTheTarget(t *testing.T) {
	text := "[meta]\nnote = \"\"\"\n[gate.slots]\nheavy = 1\n\"\"\"\n[gate.slots]\nheavy = 3\n"
	p := cfgFile(t, text)
	_, err := Set(p, "", "slots.heavy", "2")
	require.NoError(t, err)
	assert.Equal(t, "[meta]\nnote = \"\"\"\n[gate.slots]\nheavy = 1\n\"\"\"\n[gate.slots]\nheavy = 2\n", read(t, p))
	cfg, _ := Load(p)
	assert.Equal(t, 2, cfg.Slots.Heavy)
}

// A key that holds a dot is one key, not the path gate.slots.
func TestSet_AQuotedKeyWithADotIsNotTheTable(t *testing.T) {
	text := "[\"gate.slots\"]\nheavy = 1\n"
	p := cfgFile(t, text)
	_, err := Set(p, "", "slots.heavy", "2")
	if err != nil {
		assert.Equal(t, text, read(t, p))
		return
	}
	assert.Contains(t, read(t, p), "[\"gate.slots\"]\nheavy = 1\n", "the other table is as it was")
}

// Whatever the layout, a set that would change anything else is refused (the safety net under the line editor).
func TestOnlyChanged(t *testing.T) {
	path := []string{"gate", "slots", "heavy"}
	assert.True(t, onlyChanged("[gate.slots]\nheavy = 1\nlight = 3\n", "[gate.slots]\nheavy = 2\nlight = 3\n", path))
	assert.True(t, onlyChanged("", "[gate.slots]\nheavy = 2\n", path))
	assert.False(t, onlyChanged("[gate.slots]\nheavy = 1\nlight = 3\n", "[gate.slots]\nheavy = 2\nlight = 4\n", path))
	assert.False(t, onlyChanged("[m]\nx = \"a\"\n[gate.slots]\nheavy = 1\n", "[m]\nx = \"b\"\n[gate.slots]\nheavy = 1\n", path))
	assert.False(t, onlyChanged("[gate.slots]\nheavy = 1\n", "[gate.slots]\nheavy = 2\n[extra]\ny = 1\n", path))
	assert.False(t, onlyChanged("[gate.slots]\nheavy = 1\n", "[gate.slots\n", path))
}
