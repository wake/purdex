// Package labels derives a host's automatic labels and toolchain fingerprint
// from its facts (spec §7). Earned labels, qualification and drift are not here.
package labels

import (
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/wake/purdex/internal/wwd/wire"
)

// reMinorSuffix is what may follow the minor's digits: nothing, or a Go-style
// pre-release such as rc1 (go1.26rc1). Any other tail is not a version.
var reMinorSuffix = regexp.MustCompile(`^((rc|beta|alpha)[0-9]{1,3})?$`)

// maxVersionDigits bounds a version number: a label like tool:node24 never has
// more than four digits, and anything longer is not a version.
const maxVersionDigits = 4

func plainNumber(s string) (int, bool) {
	if s == "" || len(s) > maxVersionDigits {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// versionParts reads the numbers a label needs from a version string
// ("24.14.0", "16.2", "24.14.0-rc1"): the major, and when needMinor the minor
// (its leading digits). A part that is missing, signed, not a number or too
// long refuses the whole version: a missing minor is not minor 0.
func versionParts(v string, needMinor bool) (major, minor int, ok bool) {
	f := strings.SplitN(strings.TrimSpace(v), ".", 3)
	major, ok = plainNumber(f[0])
	if !ok {
		return 0, 0, false
	}
	if !needMinor {
		return major, 0, true
	}
	if len(f) < 2 {
		return 0, 0, false
	}
	digits := f[1]
	end := 0
	for end < len(digits) && digits[end] >= '0' && digits[end] <= '9' {
		end++
	}
	minor, ok = plainNumber(digits[:end])
	if !ok || !reMinorSuffix.MatchString(digits[end:]) {
		return 0, 0, false
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
	one := func(key, name string, v *string, needMinor bool, label func(major, minor int) string) {
		if v == nil {
			return
		}
		if major, minor, ok := versionParts(*v, needMinor); ok {
			labels = append(labels, "tool:"+name+label(major, minor))
			fingerprint[key] = strings.TrimSpace(*v)
		}
	}
	major := func(maj, _ int) string { return strconv.Itoa(maj) }
	one("go", "go", f.Tools.Go, true, func(maj, min int) string { return strconv.Itoa(maj) + "." + strconv.Itoa(min) })
	one("node", "node", f.Tools.Node, false, major)
	one("pnpm", "pnpm", f.Tools.Pnpm, false, major)
	one("xcode", "xcode", f.Tools.Xcode, false, major)
	// claude: the label names the tool only. The fingerprint keeps the major version: Claude Code ships a patch nearly every day, and a patch
	// must not take back the labels earned under it (drift.Changed); a major is a change worth qualifying again.
	if f.Tools.Claude != nil {
		if maj, _, ok := versionParts(*f.Tools.Claude, false); ok {
			labels = append(labels, "tool:claude")
			fingerprint["claude"] = strconv.Itoa(maj)
		}
	}
	var php []string
	for _, v := range f.Tools.PHP {
		if maj, min, ok := versionParts(v, true); ok {
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
