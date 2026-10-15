package gate

import "path/filepath"

// Module 7, cache: `go test -count=1` switches Go's test cache off, so every run repeats work the cache already did.
// Below the release tier that is waste: warn about it, or take it out.
type CacheConfig struct {
	Count1 string // "off": leave it; "warn": say so on stderr; "strip": take -count=1 out
}

func parseCache(r raw) CacheConfig {
	return CacheConfig{Count1: r.Enum("count1", "warn", "off", "warn", "strip")}
}

// Apply looks at a `go test` command for -count=1 and returns the words to run and a note for stderr ("" when there is
// nothing to say). foreground (--fg, the release gate) is never touched.
func (c CacheConfig) Apply(argv []string, foreground bool) (out []string, note string) {
	if c.Count1 == "off" || foreground || len(argv) < 2 || filepath.Base(argv[0]) != "go" || argv[1] != "test" {
		return argv, ""
	}
	found := false
	out = make([]string, 0, len(argv))
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "-count=1" || a == "--count=1":
			found = true
			continue
		case (a == "-count" || a == "--count") && i+1 < len(argv) && argv[i+1] == "1":
			found = true
			i++
			continue
		}
		out = append(out, a)
	}
	if !found {
		return argv, ""
	}
	if c.Count1 == "strip" {
		return out, "-count=1 taken out (it switches off Go's test cache; use --fg for a release run)"
	}
	return argv, "-count=1 switches off Go's test cache, so this run repeats work the cache has (use --fg for a release run, or set [gate.cache] count1)"
}
