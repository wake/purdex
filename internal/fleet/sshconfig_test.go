package fleet

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// sshHome makes a home directory whose ~/.ssh holds the given files (name relative to ~/.ssh -> content).
func sshHome(t *testing.T, files map[string]string) string {
	t.Helper()
	home := t.TempDir()
	for name, content := range files {
		p := filepath.Join(home, ".ssh", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func aliasesOf(t *testing.T, home string) []string {
	t.Helper()
	got, err := Aliases(home)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// Literal Host names are aliases; patterns (`*`, `?`, `!`) are not, and neither is a name the alias rule refuses.
// Mutation gate: accept a name with `*` -> the wildcard entry is listed (red).
func TestAliases_LiteralHostNamesOnly(t *testing.T) {
	home := sshHome(t, map[string]string{"config": `
# a comment
Host air19
  HostName 100.64.0.1
  IdentityFile ~/.ssh/id_air2019

Host mserv mserv-lan
  User wake

Host *
  ServerAliveInterval 30

Host *.internal gw?
Host !blocked ok-name
host   lower.case_ok
Host has/slash has@at
Host ` + strings.Repeat("a", 65) + ` ` + strings.Repeat("b", 64) + `
`})
	got := aliasesOf(t, home)
	want := []string{"air19", "mserv", "mserv-lan", "ok-name", "lower.case_ok", strings.Repeat("b", 64)}
	if !slices.Equal(got, want) {
		t.Fatalf("aliases = %q, want %q", got, want)
	}
}

// Keyword and value may be separated by `=`, a keyword is case-insensitive, trailing comments are dropped and a name listed
// twice appears once.
func TestAliases_SyntaxVariants(t *testing.T) {
	home := sshHome(t, map[string]string{"config": "HOST=eq-form\nhost\tTabbed # not-an-alias\n  Host   spaced   \nHost eq-form\n"})
	got := aliasesOf(t, home)
	want := []string{"eq-form", "Tabbed", "spaced"}
	if !slices.Equal(got, want) {
		t.Fatalf("aliases = %q, want %q", got, want)
	}
}

// A Match block yields no alias of its own; a Host line after it is read again.
func TestAliases_MatchBlocksIgnored(t *testing.T) {
	home := sshHome(t, map[string]string{"config": "Match host fake\n  Hostname x\nMatch all\nHost real\n  User u\n"})
	got := aliasesOf(t, home)
	if !slices.Equal(got, []string{"real"}) {
		t.Fatalf("aliases = %q, want [real]", got)
	}
}

// A missing config is not an error: there are simply no aliases.
func TestAliases_MissingFileIsEmpty(t *testing.T) {
	got, err := Aliases(t.TempDir())
	if err != nil || len(got) != 0 {
		t.Fatalf("aliases = %q, %v; want none", got, err)
	}
}

// Include is followed one level: relative paths are under ~/.ssh, a glob is expanded (in name order), an absolute path and ~
// are honoured, an Include inside an included file is ignored, and a file that is not there is skipped.
func TestAliases_IncludeOneLevel(t *testing.T) {
	home := sshHome(t, map[string]string{
		"config":          "Include conf.d/*.conf extra missing-file\nInclude ~/.ssh/tilde.conf\nHost top\n",
		"conf.d/b.conf":   "Host from-b\nInclude nested.conf\n",
		"conf.d/a.conf":   "Host from-a\n",
		"conf.d/skip.txt": "Host not-matched-by-glob\n",
		"extra":           "Host from-extra\n",
		"tilde.conf":      "Host from-tilde\n",
		"nested.conf":     "Host from-nested\n",
	})
	got := aliasesOf(t, home)
	want := []string{"from-a", "from-b", "from-extra", "from-tilde", "top"}
	if !slices.Equal(got, want) {
		t.Fatalf("aliases = %q, want %q", got, want)
	}
}

// Quoted arguments are read as ssh reads them: the quotes are not part of the name or of an include path.
func TestAliases_QuotedArguments(t *testing.T) {
	home := sshHome(t, map[string]string{
		"config":         "Host \"build-box\" plain \"two words\" \"*\"\nInclude \"conf dir/hosts\"\n",
		"conf dir/hosts": "Host \"inc-box\"\n",
	})
	got := aliasesOf(t, home)
	want := []string{"build-box", "plain", "inc-box"}
	if !slices.Equal(got, want) {
		t.Fatalf("aliases = %q, want %q", got, want)
	}
	if !ValidAlias(home, "build-box") {
		t.Error("quoted alias should be valid")
	}
}

// A config ssh would refuse to load is refused here too, so validation never approves what ssh will not run.
func TestAliases_FailClosed(t *testing.T) {
	cases := map[string]map[string]string{
		"unterminated quote":      {"config": "Host \"prod\n"},
		"unterminated in include": {"config": "Include inc\nHost ok\n", "inc": "Host \"bad\n"},
		"main over 1 MiB":         {"config": "Host ok\n" + strings.Repeat("#", maxConfigBytes)},
		"include over 1 MiB":      {"config": "Include inc\nHost ok\n", "inc": "Host big\n" + strings.Repeat("#", maxConfigBytes)},
	}
	for name, files := range cases {
		home := sshHome(t, files)
		if got, err := Aliases(home); err == nil {
			t.Errorf("%s: want an error, got %q", name, got)
		}
		if ValidAlias(home, "ok") || ValidAlias(home, "prod") {
			t.Errorf("%s: ValidAlias must be false", name)
		}
	}
	// Exactly at the limit is still read.
	home := sshHome(t, map[string]string{"config": "Host ok\n" + strings.Repeat("#", maxConfigBytes-8)})
	if !ValidAlias(home, "ok") {
		t.Error("a file of exactly maxConfigBytes must be read")
	}
}

// There is no cache: a Host removed from an included file is gone at the next call, and one added is there.
// Mutation gate: cache the first read -> the removed alias is still listed (red).
func TestAliases_NoCache(t *testing.T) {
	home := sshHome(t, map[string]string{
		"config":       "Include conf.d/*\n",
		"conf.d/hosts": "Host one\nHost two\n",
		"conf.d/other": "Host three\n",
	})
	if got := aliasesOf(t, home); !slices.Equal(got, []string{"one", "two", "three"}) && !slices.Equal(got, []string{"three", "one", "two"}) {
		t.Fatalf("first read = %q", got)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "conf.d", "hosts"), []byte("Host one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := aliasesOf(t, home)
	if slices.Contains(got, "two") || !slices.Contains(got, "one") || !slices.Contains(got, "three") {
		t.Fatalf("after removing `two` from the included file: %q", got)
	}
	if err := os.Remove(filepath.Join(home, ".ssh", "conf.d", "other")); err != nil {
		t.Fatal(err)
	}
	if got := aliasesOf(t, home); slices.Contains(got, "three") {
		t.Fatalf("after deleting the included file: %q", got)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte("Host only-main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := aliasesOf(t, home); !slices.Equal(got, []string{"only-main"}) {
		t.Fatalf("after rewriting the main config: %q", got)
	}
}

// ValidAlias is the alias rule and presence in the config, read fresh.
// Mutation gate: skip the presence check -> an alias that is not in the config is valid (red); skip the rule -> a name with a
// shell metacharacter is valid when it is in the config (red).
func TestValidAlias(t *testing.T) {
	home := sshHome(t, map[string]string{"config": "Host air19 mserv\nHost *\nHost bad;name -dash\n"})
	for alias, want := range map[string]bool{
		"air19":     true,
		"mserv":     true,
		"unknown":   false, // not in the config
		"*":         false, // a pattern is not an alias
		"":          false,
		"bad;name":  false, // the rule refuses it even if it were listed
		"AIR19":     false, // names are literal and case-sensitive
		"air19 ":    false,
		"-dash":     true, // the rule allows it; Run keeps it after `--`
		"a\nb":      false,
		"air19/../": false,
	} {
		if got := ValidAlias(home, alias); got != want {
			t.Errorf("ValidAlias(%q) = %v, want %v", alias, got, want)
		}
	}
	// the config changes: the next call sees it
	if err := os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte("Host mserv\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ValidAlias(home, "air19") {
		t.Error("air19 stays valid after it left the config")
	}
}
