package execution

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanonicalize_ResolvesSymlinkToRealPath(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real-repo")
	require.NoError(t, os.Mkdir(real, 0o755))
	link := filepath.Join(root, "link-repo")
	require.NoError(t, os.Symlink(real, link))

	fromReal, err := Canonicalize(real, nil)
	require.NoError(t, err)
	fromLink, err := Canonicalize(link, nil)
	require.NoError(t, err)

	// Symlink alias and the real path must map to the SAME canonical key so a
	// single-live rule cannot be bypassed by dispatching against the alias.
	require.Equal(t, fromReal, fromLink)
}

func TestCanonicalize_NormalisesDotDotAndTrailingSlash(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	require.NoError(t, os.Mkdir(repo, 0o755))

	// repo/../repo/  should normalise to the same key as repo.
	messy := filepath.Join(repo, "..", "repo") + string(filepath.Separator)
	clean, err := Canonicalize(repo, nil)
	require.NoError(t, err)
	fromMessy, err := Canonicalize(messy, nil)
	require.NoError(t, err)
	require.Equal(t, clean, fromMessy)
}

func TestCanonicalize_ReturnsAbsoluteResolvedPath(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	require.NoError(t, os.Mkdir(repo, 0o755))

	got, err := Canonicalize(repo, nil)
	require.NoError(t, err)
	require.True(t, filepath.IsAbs(got), "canonical path must be absolute, got %q", got)

	// The resolved path itself resolves to itself (idempotent / fully resolved).
	resolved, err := filepath.EvalSymlinks(repo)
	require.NoError(t, err)
	require.Equal(t, resolved, got)
}

func TestCanonicalize_RejectsEmpty(t *testing.T) {
	_, err := Canonicalize("", nil)
	require.ErrorIs(t, err, ErrCanonical)
}

func TestCanonicalize_RejectsNonExistent(t *testing.T) {
	_, err := Canonicalize(filepath.Join(t.TempDir(), "does-not-exist"), nil)
	require.ErrorIs(t, err, ErrCanonical)
}

func TestCanonicalize_WithinAllowedRoot(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	require.NoError(t, os.Mkdir(repo, 0o755))

	got, err := Canonicalize(repo, []string{root})
	require.NoError(t, err)
	require.Equal(t, mustEval(t, repo), got)
}

func TestCanonicalize_RejectsEscapeViaDotDot(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "allowed")
	outside := filepath.Join(base, "outside")
	require.NoError(t, os.Mkdir(allowed, 0o755))
	require.NoError(t, os.Mkdir(outside, 0o755))

	// allowed/../outside escapes the allowlist root and must be rejected.
	escape := filepath.Join(allowed, "..", "outside")
	_, err := Canonicalize(escape, []string{allowed})
	require.ErrorIs(t, err, ErrCanonical)
}

func TestCanonicalize_RejectsEscapeViaSymlink(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "allowed")
	outside := filepath.Join(base, "outside")
	require.NoError(t, os.Mkdir(allowed, 0o755))
	require.NoError(t, os.Mkdir(outside, 0o755))

	// A symlink INSIDE the allowed root pointing OUTSIDE must be rejected: the
	// real (resolved) target escapes the allowlist.
	link := filepath.Join(allowed, "sneaky")
	require.NoError(t, os.Symlink(outside, link))
	_, err := Canonicalize(link, []string{allowed})
	require.ErrorIs(t, err, ErrCanonical)
}

func TestCanonicalize_AllowedRootItselfAccepted(t *testing.T) {
	root := t.TempDir()
	got, err := Canonicalize(root, []string{root})
	require.NoError(t, err)
	require.Equal(t, mustEval(t, root), got)
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	require.NoError(t, err)
	return r
}
