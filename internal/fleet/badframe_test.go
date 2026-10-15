package fleet

import (
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsBadFrame(t *testing.T) {
	assert.True(t, IsBadFrame(ErrFrameTooBig))
	assert.True(t, IsBadFrame(fmt.Errorf("x: %w", ErrIncompleteFrame)))
	assert.False(t, IsBadFrame(io.EOF))
	assert.False(t, IsBadFrame(nil))
}
