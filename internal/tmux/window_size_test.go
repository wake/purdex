package tmux

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseWindowSize(t *testing.T) {
	c, r, err := parseWindowSize("120 40\n")
	require.NoError(t, err)
	assert.Equal(t, uint16(120), c)
	assert.Equal(t, uint16(40), r)

	for _, bad := range []string{"", "abc", "120", "0 0", "70000 10", "-1 5"} {
		_, _, err := parseWindowSize(bad)
		assert.Error(t, err, bad)
	}
}

func TestFakeExecutor_WindowSize(t *testing.T) {
	f := NewFakeExecutor()
	f.SetWindowSize(100, 30)
	c, r, err := f.WindowSize(context.Background(), "dev")
	require.NoError(t, err)
	assert.Equal(t, [2]uint16{100, 30}, [2]uint16{c, r})

	f.SetWindowSizeErr(errors.New("boom"))
	_, _, err = f.WindowSize(context.Background(), "dev")
	assert.EqualError(t, err, "boom")
}

func TestFakeExecutor_WindowSizeBlocksUntilCtxCancel(t *testing.T) {
	f := NewFakeExecutor()
	f.BlockWindowSize(true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := f.WindowSize(ctx, "dev")
		done <- err
	}()
	select {
	case <-done:
		t.Fatal("returned before cancel")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("did not return after cancel")
	}
}
