package hostconfig

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckPath(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))
	home := func() (string, error) { return dir, nil }

	got, err := checkPath(dir, home)
	require.NoError(t, err)
	assert.Equal(t, "dir", got.Status)
	assert.Equal(t, dir, got.Resolved)

	got, err = checkPath(file, home)
	require.NoError(t, err)
	assert.Equal(t, "not_dir", got.Status)

	got, err = checkPath(filepath.Join(dir, "nope"), home)
	require.NoError(t, err)
	assert.Equal(t, "missing", got.Status)

	got, err = checkPath("~", home)
	require.NoError(t, err)
	assert.Equal(t, "dir", got.Status)
	assert.Equal(t, dir, got.Resolved)

	got, err = checkPath("~/f.txt", home)
	require.NoError(t, err)
	assert.Equal(t, "not_dir", got.Status)
	assert.Equal(t, file, got.Resolved)

	got, err = checkPath("  "+dir+"/./  ", home)
	require.NoError(t, err)
	assert.Equal(t, dir, got.Resolved)
}

func TestCheckPathRejects(t *testing.T) {
	home := func() (string, error) { return "/home/x", nil }
	nul := "/a" + string(rune(0)) + "b"
	for _, p := range []string{"", "   ", "relative/dir", "~bob", nul} {
		_, err := checkPath(p, home)
		assert.Error(t, err, p)
	}
	_, err := checkPath("~/x", func() (string, error) { return "", errors.New("no home") })
	assert.Error(t, err)
}

func TestCheckPathPermissionError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permissions")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	require.NoError(t, os.Mkdir(locked, 0o000))
	t.Cleanup(func() { os.Chmod(locked, 0o700) })

	got, err := checkPath(filepath.Join(locked, "child"), func() (string, error) { return dir, nil })
	require.NoError(t, err)
	assert.Equal(t, "error", got.Status)
	assert.NotEmpty(t, got.Reason)
}
