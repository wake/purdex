package opencode

import "testing"

// SetResolveCanonicalPdxPathForTesting overrides the canonical pdx path
// resolver for the duration of t. The previous implementation is restored
// via t.Cleanup. Exported purely so the _test package can drive the
// Finding #3 path-only-edit regression from outside the package. Kept in
// a *_test.go file so the testing dependency stays out of production
// binaries.
func SetResolveCanonicalPdxPathForTesting(t *testing.T, fn func() (string, bool)) {
	t.Helper()
	prev := resolveCanonicalPdxPath
	resolveCanonicalPdxPath = fn
	t.Cleanup(func() { resolveCanonicalPdxPath = prev })
}

// RenderManagedPluginForTesting exposes renderManagedPlugin to the
// external _test package so the drift-detection contract test (#715,
// 2026-04-29 plan §T3.5) can synthesize a pre-fix managed body by
// string-substitution against the current fixed render. Test-only —
// production callers go through InstallHooks.
func RenderManagedPluginForTesting(pdxPath string) string {
	return renderManagedPlugin(pdxPath)
}
