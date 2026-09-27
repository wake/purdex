// internal/tmux/noserver_test.go
package tmux_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/tmux"
)

// localisedENOENT stands in for strerror(ENOENT) under a non-English locale:
// the reason text no longer matches, so only the stat of the path can tell
// that the socket is absent (#1473 spec D1).
const localisedENOENT = "Aucun fichier ou dossier de ce type"

func TestIsNoServer(t *testing.T) {
	tmp := t.TempDir()
	missing := filepath.Join(tmp, "missing-sock")
	existing := filepath.Join(tmp, "existing-sock")
	if err := os.WriteFile(existing, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(tmp, "dangling")
	if err := os.Symlink(missing, dangling); err != nil {
		t.Fatal(err)
	}
	parenDir := filepath.Join(tmp, "a (b)")
	if err := os.Mkdir(parenDir, 0o700); err != nil {
		t.Fatal(err)
	}
	parenMissing := filepath.Join(parenDir, "missing")

	cases := []struct {
		name   string
		stderr string
		want   bool
	}{
		{"stale socket", "no server running on /tmp/x", true},
		{"absent socket", "error connecting to " + missing + " (No such file or directory)", true},
		{"localised reason, missing path (stat)", "error connecting to " + missing + " (" + localisedENOENT + ")", true},
		{"ENOENT reason, existing path (reason)", "error connecting to " + existing + " (No such file or directory)", true},
		{"permission denied on existing path", "error connecting to " + existing + " (Permission denied)", false},
		{"dangling symlink, localised reason", "error connecting to " + dangling + " (" + localisedENOENT + ")", true},
		{"relative missing path, localised reason", "error connecting to rel/missing (" + localisedENOENT + ")", false},
		{"path containing ' ('", "error connecting to " + parenMissing + " (" + localisedENOENT + ")", true},
		{"no trailing paren", "error connecting to " + missing + " (No such file or directory", false},
		{"no ' (' delimiter", "error connecting to " + missing, false},
		{"matching line second", "some warning\nerror connecting to " + missing + " (No such file or directory)\n", true},
		{"CRLF line ending", "error connecting to " + missing + " (No such file or directory)\r\n", true},
		{"unrelated text", "something else", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tmux.IsNoServer(tc.stderr); got != tc.want {
				t.Errorf("IsNoServer(%q) = %v, want %v", tc.stderr, got, tc.want)
			}
		})
	}
}

// TestIsNoServer_StatEACCES: a stat that fails for any reason other than
// "does not exist" is not evidence that the socket is absent, so a localised
// reason on an unreadable path stays an error.
func TestIsNoServer_StatEACCES(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	d := filepath.Join(t.TempDir(), "d")
	if err := os.Mkdir(d, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(d, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(d, 0o700) })

	stderr := "error connecting to " + filepath.Join(d, "sock") + " (" + localisedENOENT + ")"
	if tmux.IsNoServer(stderr) {
		t.Errorf("IsNoServer(%q) = true, want false (stat EACCES is not ENOENT)", stderr)
	}
}
