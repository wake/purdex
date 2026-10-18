// Package labels derives a host's automatic labels and toolchain fingerprint
// from its facts (spec §7). Earned labels, qualification and drift are not here.
package labels

import (
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/wake/purdex/internal/wwd/wire"
)

// versionParts reads the leading numbers of a version string ("24.14.0", "16.2").
func versionParts(v string) (major, minor int, ok bool) {
	f := strings.SplitN(strings.TrimSpace(v), ".", 3)
	major, err := strconv.Atoi(f[0])
	if err != nil || major < 0 {
		return 0, 0, false
	}
	if len(f) > 1 {
		if m, err := strconv.Atoi(f[1]); err == nil && m >= 0 {
			minor = m
		}
	}
	return major, minor, true
}

// FromFacts is the sorted automatic labels and the fingerprint (OS version and
// the full version of every tool that answered) of one record. A tool that is
// missing or whose version does not read gives no label and no fingerprint
// entry; nothing here can fail.
func FromFacts(f wire.Facts) (labels []string, fingerprint map[string]string) {
	labels, fingerprint = []string{}, map[string]string{}
	if f.Platform != "" {
		labels = append(labels, "os:"+f.Platform)
	}
	if f.Arch != "" {
		labels = append(labels, "arch:"+f.Arch)
	}
	if f.OSVersion != "" {
		fingerprint["os_version"] = f.OSVersion
	}
	one := func(key, name string, v *string, label func(major, minor int) string) {
		if v == nil {
			return
		}
		if major, minor, ok := versionParts(*v); ok {
			labels = append(labels, "tool:"+name+label(major, minor))
			fingerprint[key] = strings.TrimSpace(*v)
		}
	}
	major := func(maj, _ int) string { return strconv.Itoa(maj) }
	one("go", "go", f.Tools.Go, func(maj, min int) string { return strconv.Itoa(maj) + "." + strconv.Itoa(min) })
	one("node", "node", f.Tools.Node, major)
	one("pnpm", "pnpm", f.Tools.Pnpm, major)
	one("xcode", "xcode", f.Tools.Xcode, major)
	var php []string
	for _, v := range f.Tools.PHP {
		if maj, min, ok := versionParts(v); ok {
			labels = append(labels, "tool:php"+strconv.Itoa(maj)+strconv.Itoa(min))
			php = append(php, strings.TrimSpace(v))
		}
	}
	if len(php) > 0 {
		sort.Strings(php)
		fingerprint["php"] = strings.Join(php, ",")
	}
	sort.Strings(labels)
	return slices.Compact(labels), fingerprint
}

// Tracker holds the current labels and fingerprint, updated from every facts
// record. Safe for concurrent use.
type Tracker struct {
	mu     sync.Mutex
	seen   bool
	labels []string
	fp     map[string]string
}

// Update takes one record (and any labels from other sources) and reports whether the labels changed (the first
// record always does). The fingerprint follows silently: a patch version moves
// it without moving any label.
func (t *Tracker) Update(f wire.Facts, extra ...string) bool {
	l, fp := FromFacts(f)
	if len(extra) > 0 { // labels from other sources, e.g. repo:<name>
		l = append(l, extra...)
		sort.Strings(l)
		l = slices.Compact(l)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	changed := !t.seen || !slices.Equal(t.labels, l)
	t.seen, t.labels, t.fp = true, l, fp
	return changed
}

// Current is a copy of the labels and the fingerprint (empty, not nil, before
// the first record).
func (t *Tracker) Current() ([]string, map[string]string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	fp := make(map[string]string, len(t.fp))
	for k, v := range t.fp {
		fp[k] = v
	}
	return append([]string{}, t.labels...), fp
}
