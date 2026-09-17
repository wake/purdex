package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveCwd covers the expansion + validation that stands between an API
// caller and `tmux new-session -c`. tmux neither expands ~ nor fails on an
// unusable -c: it silently starts the session in $HOME, so every rejection
// here is a bug the daemon would otherwise hide.
//
// Every case that is expected to SUCCEED is built from t.TempDir(), never from
// a literal path — the os.Stat gate would otherwise make the test depend on
// what happens to exist on the machine running it.
func TestResolveCwd(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "Workspace", "x"), 0o755))

	other := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(other, "dir"), 0o755))
	regularFile := filepath.Join(other, "file.txt")
	require.NoError(t, os.WriteFile(regularFile, []byte("x"), 0o644))

	okHome := func() (string, error) { return home, nil }
	failHome := func() (string, error) { return "", errors.New("no home") }
	emptyHome := func() (string, error) { return "", nil }

	tests := []struct {
		name    string
		raw     string
		home    func() (string, error)
		want    string
		wantErr bool
	}{
		{name: "empty defaults to root", raw: "", home: okHome, want: "/"},
		{name: "blank defaults to root", raw: "   ", home: okHome, want: "/"},
		{name: "bare tilde is home", raw: "~", home: okHome, want: home},
		{
			name: "tilde slash expands",
			raw:  "~/Workspace/x",
			home: okHome,
			want: filepath.Join(home, "Workspace", "x"),
		},
		{
			name: "tilde slash is cleaned",
			raw:  "~/Workspace/../Workspace/x",
			home: okHome,
			want: filepath.Join(home, "Workspace", "x"),
		},
		{name: "tilde user form is rejected", raw: "~foo/bar", home: okHome, wantErr: true},
		{name: "relative path is rejected", raw: "relative/x", home: okHome, wantErr: true},
		{name: "home lookup failure is an error", raw: "~/x", home: failHome, wantErr: true},
		{name: "empty home is an error", raw: "~/x", home: emptyHome, wantErr: true},
		{name: "NUL byte is rejected", raw: "/tmp/a\x00b", home: okHome, wantErr: true},
		{name: "existing dir passes through", raw: filepath.Join(other, "dir"), home: okHome, want: filepath.Join(other, "dir")},
		{name: "existing file is rejected", raw: regularFile, home: okHome, wantErr: true},
		{name: "missing path is rejected", raw: filepath.Join(other, "nope", "gone"), home: okHome, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveCwd(tc.raw, tc.home)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestResolveCwdDoesNotCallHomeForAbsolutePaths guards against a resolver that
// fails on machines with no resolvable home even when the caller never asked
// for ~ expansion.
func TestResolveCwdDoesNotCallHomeForAbsolutePaths(t *testing.T) {
	dir := t.TempDir()
	called := false
	got, err := resolveCwd(dir, func() (string, error) {
		called = true
		return "", errors.New("no home")
	})
	require.NoError(t, err)
	assert.Equal(t, dir, got)
	assert.False(t, called, "home lookup must not run for an absolute path")
}
