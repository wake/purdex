package config

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// #1950: SetToken writes the daemon's token into the config file by changing that one line: comments, unknown keys and tables
// stay as they were, byte for byte. It takes a lock on the file for its whole read-modify-write and writes through a temp file of
// its own, so two writers (two daemons sharing a config, `pdx token generate` beside a starting daemon) cannot lose each other's
// update or corrupt the file.

const tok = "purdex_0123456789abcdef0123456789abcdef01234567"

func setup(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	return p
}

func TestSetToken_ChangesOnlyTheTokenLine(t *testing.T) {
	orig := "# my daemon\nbind = \"100.64.0.2\" # tailnet\nport = 7860\ntoken = \"\"\nallow = [\"100.64.0.0/10\"]\n\n[[stream.presets]]\nname = \"legacy\"\n\n# a comment between\n[dispatch]\nx = 1\n"
	p := setup(t, orig)
	require.NoError(t, SetToken(p, tok))
	got, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Equal(t, strings.Replace(orig, `token = ""`, `token = "`+tok+`"`, 1), string(got))
	st, _ := os.Stat(p)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())
	cfg, err := Load(p)
	require.NoError(t, err)
	require.Equal(t, tok, cfg.Token)
}

func TestSetToken_ReplacesAnExistingTokenValue(t *testing.T) {
	p := setup(t, "port = 7860\ntoken = \"pdx_old\"\nbind = \"127.0.0.1\"\n")
	require.NoError(t, SetToken(p, tok))
	got, _ := os.ReadFile(p)
	require.Equal(t, "port = 7860\ntoken = \""+tok+"\"\nbind = \"127.0.0.1\"\n", string(got))
}

func TestSetToken_InsertsAmongTheTopLevelKeysBeforeTheFirstTable(t *testing.T) {
	p := setup(t, "# c\nport = 7860\n\n[peers]\nx = 1\n")
	require.NoError(t, SetToken(p, tok))
	got, _ := os.ReadFile(p)
	require.Equal(t, "# c\nport = 7860\n\ntoken = \""+tok+"\"\n[peers]\nx = 1\n", string(got))
	cfg, err := Load(p)
	require.NoError(t, err)
	require.Equal(t, tok, cfg.Token, "a top-level key, not a key of [peers]")
}

func TestSetToken_AppendsWhenThereIsNoTable(t *testing.T) {
	p := setup(t, "port = 7860") // no trailing newline
	require.NoError(t, SetToken(p, tok))
	got, _ := os.ReadFile(p)
	require.Equal(t, "port = 7860\ntoken = \""+tok+"\"\n", string(got))
}

// What the line edit cannot do safely it does not do: a multi-line string before the first table, or an edit that would not read
// back as the token (a quoted key that is a duplicate), falls back to the whole-file rewrite — the file still reads back right.
func TestSetToken_FallsBackToARewriteWhenALineEditIsNotSafe(t *testing.T) {
	for name, content := range map[string]string{
		"multi-line string": "note = \"\"\"\ntoken = \"fake\"\n\"\"\"\nport = 7861\n",
		"quoted duplicate":  "\"token\" = \"old\"\nport = 7861\n",
		"two token lines":   "token = \"a\"\ntoken = \"b\"\n",
	} {
		p := setup(t, content)
		if name == "two token lines" { // not valid TOML to begin with: the rewrite cannot read it either
			require.Error(t, SetToken(p, tok), name)
			continue
		}
		require.NoError(t, SetToken(p, tok), name)
		cfg, err := Load(p)
		require.NoError(t, err, name)
		require.Equal(t, tok, cfg.Token, name)
		require.Equal(t, 7861, cfg.Port, name)
	}
}

func TestSetToken_CreatesAMissingFileWithTheDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "deep", "config.toml")
	require.NoError(t, SetToken(p, tok))
	cfg, err := Load(p)
	require.NoError(t, err)
	require.Equal(t, tok, cfg.Token)
	require.Equal(t, 7860, cfg.Port)
	st, _ := os.Stat(p)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())
}

func TestSetToken_ACannotBeWrittenPathIsAnErrorThatNamesNoToken(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))
	err := SetToken(filepath.Join(blocker, "config.toml"), tok)
	require.Error(t, err)
	require.NotContains(t, err.Error(), tok)
}

// Many writers on one file: the file always reads back, holds one of the tokens, and no temp file is left. Mutation gate: the lock
// removed or a shared temp name → a torn file or a lost rename (red, with enough rounds).
func TestSetToken_ConcurrentWritersNeverTearTheFile(t *testing.T) {
	p := setup(t, "# keep me\nport = 7860\ntoken = \"\"\n\n[peers]\nx = 1\n")
	var wg sync.WaitGroup
	tokens := make([]string, 16)
	for i := range tokens {
		tokens[i] = "purdex_" + strings.Repeat(string(rune('a'+i)), 40)
	}
	for _, tk := range tokens {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				require.NoError(t, SetToken(p, tk))
			}
		}()
	}
	wg.Wait()
	cfg, err := Load(p)
	require.NoError(t, err)
	require.Contains(t, tokens, cfg.Token)
	got, _ := os.ReadFile(p)
	require.True(t, strings.HasPrefix(string(got), "# keep me\nport = 7860\ntoken = \""), "%s", got)
	require.Contains(t, string(got), "[peers]\nx = 1\n")
	ents, _ := os.ReadDir(filepath.Dir(p))
	for _, e := range ents {
		require.NotContains(t, e.Name(), ".tmp", "a temp file was left")
	}
}

// WriteFile has no lock (core.UpdateConfig and others call it): its temp file must be its own, or two writers share one and a
// rename finds it gone / publishes a mix. Mutation gate: a fixed temp name → an error or a torn file (red).
func TestWriteFile_ConcurrentWritersNeverShareATempFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 40 {
				require.NoError(t, WriteFile(p, Config{Bind: "127.0.0.1", Port: 7000 + i, Token: "pdx_x"}))
			}
		}()
	}
	wg.Wait()
	cfg, err := Load(p)
	require.NoError(t, err)
	require.GreaterOrEqual(t, cfg.Port, 7000)
	ents, _ := os.ReadDir(filepath.Dir(p))
	require.Len(t, ents, 1, "only config.toml is left")
}
