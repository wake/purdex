package gate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ptr(s string) *string { return &s }

// A write that carries the value the caller read goes through; one that carries a stale value is refused and changes nothing.
func TestSetExpect_WritesOnlyWhenTheValueIsTheOneRead(t *testing.T) {
	p := cfgFile(t, "[gate.slots]\nheavy = 2\n")
	_, err := SetExpect(p, "", "slots.heavy", "4", ptr("1"))
	var ee *ExpectError
	require.True(t, errors.As(err, &ee))
	assert.Equal(t, "2", ee.Have)
	assert.Equal(t, "1", ee.Want)
	assert.Contains(t, read(t, p), "heavy = 2", "the file is as it was")

	res, err := SetExpect(p, "", "slots.heavy", "4", ptr(" 2 "))
	require.NoError(t, err)
	assert.Equal(t, "2", res.Old)
	assert.Equal(t, "4", res.New)
	assert.Contains(t, read(t, p), "heavy = 4")
}

// Without --expect a write is what it always was (no comparison).
func TestSetExpect_NilExpectIsAPlainSet(t *testing.T) {
	p := cfgFile(t, "[gate.slots]\nheavy = 2\n")
	_, err := SetExpect(p, "", "slots.heavy", "3", nil)
	require.NoError(t, err)
	assert.Contains(t, read(t, p), "heavy = 3")
}

// The default (nothing in the file) is a value too: a writer that read "1" from the defaults can lower it.
func TestSetExpect_TheDefaultCountsAsTheCurrentValue(t *testing.T) {
	p := cfgFile(t, "")
	d := Defaults().Values["slots.heavy"]
	_, err := SetExpect(p, "", "slots.heavy", "5", ptr(d))
	require.NoError(t, err)
	_, err = SetExpect(p, "", "slots.heavy", "1", ptr(d))
	assert.Error(t, err, "it is 5 now")
}

// A config reached through a symlink and through its real path is one file with one lock.
func TestSetExpect_ASymlinkedConfigIsLockedByItsRealPath(t *testing.T) {
	real := cfgFile(t, "[gate.slots]\nheavy = 2\n")
	link := filepath.Join(t.TempDir(), "link.toml")
	require.NoError(t, os.Symlink(real, link))
	_, err := SetExpect(link, "", "slots.heavy", "4", ptr("2"))
	require.NoError(t, err)
	assert.Contains(t, read(t, real), "heavy = 4")
	_, err = os.Stat(real + ".lock")
	assert.NoError(t, err, "the lock sits beside the real file")
	_, err = os.Stat(link + ".lock")
	assert.True(t, os.IsNotExist(err), "and not beside the link")
}

// An active profile that sets the key makes a [gate] write pointless, and an --expect write would compare the wrong layer: refused.
func TestSetExpect_AnActiveProfilesValueIsNotTheLayerBeingWritten(t *testing.T) {
	p := cfgFile(t, "[gate]\nprofile = \"night\"\n[gate.slots]\nheavy = 4\n[gate.profiles.night.slots]\nheavy = 2\n")
	before := read(t, p)
	_, err := SetExpect(p, "", "slots.heavy", "3", ptr("2"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "active profile")
	assert.Equal(t, before, read(t, p))
	_, err = SetExpect(p, "night", "slots.heavy", "3", ptr("2"))
	require.NoError(t, err, "the profile written to is the layer compared")
}

func TestSetExpect_TheReadBackIsTakenUnderTheLock(t *testing.T) {
	p := cfgFile(t, "[gate.slots]\nheavy = 2\n")
	res, err := SetExpect(p, "", "slots.heavy", "5", ptr("2"))
	require.NoError(t, err)
	assert.Equal(t, "5", res.ReadBack)
	res, err = SetExpect(p, "", "slots.heavy", "6", nil)
	require.NoError(t, err)
	assert.Empty(t, res.ReadBack, "only an --expect write reads back")
}

func TestSetExpect_AProfileWriteIsReadBackToo(t *testing.T) {
	p := cfgFile(t, "[gate.profiles.night.slots]\nheavy = 2\n")
	res, err := SetExpect(p, "night", "slots.heavy", "3", ptr("2"))
	require.NoError(t, err)
	assert.Equal(t, "3", res.ReadBack)
}

// Two names for a config that is not there yet (a symlinked parent) share one lock; a dangling symlink is not replaced.
func TestSetExpect_ASymlinkedParentOfANewConfigSharesTheRealLock(t *testing.T) {
	realDir := t.TempDir()
	linkDir := filepath.Join(t.TempDir(), "d")
	require.NoError(t, os.Symlink(realDir, linkDir))
	_, err := SetExpect(filepath.Join(linkDir, "config.toml"), "", "slots.heavy", "3", nil)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(realDir, "config.toml.lock"))
	assert.NoError(t, err, "the lock is in the real directory")
	_, err = os.Stat(filepath.Join(realDir, "config.toml"))
	assert.NoError(t, err)

	dang := filepath.Join(t.TempDir(), "dangling.toml")
	require.NoError(t, os.Symlink(filepath.Join(t.TempDir(), "nowhere.toml"), dang))
	_, err = SetExpect(dang, "", "slots.heavy", "3", nil)
	require.Error(t, err)
	fi, lerr := os.Lstat(dang)
	require.NoError(t, lerr)
	assert.NotZero(t, fi.Mode()&os.ModeSymlink, "the symlink is still a symlink")
}
