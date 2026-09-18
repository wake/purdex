package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHandoffLocks_TryLockAndUnlock(t *testing.T) {
	locks := NewHandoffLocks()

	assert.True(t, locks.TryLock("abc"), "first lock should succeed")
	assert.False(t, locks.TryLock("abc"), "second lock should fail")

	locks.Unlock("abc")
	assert.True(t, locks.TryLock("abc"), "lock after unlock should succeed")
}

func TestHandoffLocks_IndependentKeys(t *testing.T) {
	locks := NewHandoffLocks()

	assert.True(t, locks.TryLock("a"))
	assert.True(t, locks.TryLock("b"))
	assert.False(t, locks.TryLock("a"))
	assert.True(t, locks.TryLock("c"))
}
