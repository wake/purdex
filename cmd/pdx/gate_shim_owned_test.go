package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shimMarker is the fixed start of the second line of every shim an old setup wrote (the header the removal tests forge variants of).
const shimMarker = "# Written by `pdx setup` (pdx gate shim for"

// fakeShim is a shim of an old `pdx setup`.
func fakeShim(tool string) string { return shimScript(tool, "/nonexistent/pdx") }

// writeExe writes an executable file (and the directories above it).
func writeExe(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o755))
}

func TestShimToolsHaveComposerAndNotThePHPRunners(t *testing.T) {
	assert.Contains(t, shimTools, "composer")
	assert.NotContains(t, shimTools, "phpunit")
	assert.NotContains(t, shimTools, "pest")
	assert.Len(t, shimTools, 10)
}

// shimScript must stay byte for byte what the retired generator wrote (setup deletes a file only if it is exactly that). The digests are those of
// internal/gate/shim.Script as of c515fac3f, for a plain path and for one with a space and the four characters sh quoting escapes.
func TestShimScript_IsTheRetiredGeneratorByteForByte(t *testing.T) {
	for _, c := range []struct{ tool, pdx, sum string }{
		{"go", "/p/pdx", "907e62e0e476954cb21a62aba2dcf3901e551c95a52d180f1cd72746ba291325"},
		{"swift-test", "/a b/\"$`x/pdx", "5bfbd5ad4f85d26c4c5ea310844e695ff9471b8968ee21a733eb10f5365a7e2e"},
	} {
		sum := sha256.Sum256([]byte(shimScript(c.tool, c.pdx)))
		assert.Equal(t, c.sum, hex.EncodeToString(sum[:]), c.tool)
		got, ok := shimPdxPath(shimScript(c.tool, c.pdx))
		assert.True(t, ok)
		assert.Equal(t, c.pdx, got)
	}
}
