package gate

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// Editing the rules (spec §5.3, plan GC.7): every edit takes conflicts.lock, re-reads the file, decides against what it read and
// replaces the file atomically, so no edit is lost; two edits of the same rule at the same moment end as the one that took the lock
// last leaves it, and each says what it found and what it left.

const conflictsLockFile = "conflicts.lock"

// maxConflictReason is the longest reason: one short line.
const maxConflictReason = 200

// ErrConflictsBroken: conflicts.json exists but cannot be used, so an edit would write over something nobody could read.
var ErrConflictsBroken = errors.New("conflicts.json cannot be read; fix or remove it first")

// editConflicts runs edit on the rules under conflicts.lock; when edit says to write, the result replaces the file.
func (g *Gate) editConflicts(edit func(rules []Conflict) (out []Conflict, write bool, msg string)) (string, error) {
	dir := g.Config().Dir
	unlock, err := lockFile(filepath.Join(dir, conflictsLockFile))
	if err != nil {
		return "", dirError(err)
	}
	defer unlock()
	rules, problem := LoadConflicts(dir)
	if problem != "" {
		return "", fmt.Errorf("%w: %s", ErrConflictsBroken, problem)
	}
	out, write, msg := edit(rules)
	if write {
		if err := SaveConflicts(dir, out); err != nil {
			return "", err
		}
	}
	return msg, nil
}

// ConflictLine is a rule on one line: "#2 heavy: tsc -b × vitest related (reason) by sess at 14:02".
func ConflictLine(n int, c Conflict) string {
	by := c.By
	if by == "" {
		by = "?"
	}
	return fmt.Sprintf("#%d %s: %s × %s (%s) by %s at %s", n, c.Class, c.A, c.B, c.Reason, by, c.At.Local().Format("01-02 15:04"))
}

func samePair(c Conflict, class Class, a, b string) bool {
	x, y := strings.Join(patternWords(c.A), " "), strings.Join(patternWords(c.B), " ")
	p, q := strings.Join(patternWords(a), " "), strings.Join(patternWords(b), " ")
	return c.Class == class && ((x == p && y == q) || (x == q && y == p))
}

// ValidateConflict checks a rule as a person typed it: a class, two patterns with at least a tool word, a reason of one short line.
func ValidateConflict(class Class, a, b, reason string) error {
	switch {
	case class != ClassHeavy && class != ClassLight:
		return fmt.Errorf("the class must be heavy or light, not %q", string(class))
	case len(patternWords(a)) == 0 || len(patternWords(b)) == 0:
		return errors.New("a pattern must have at least a tool word")
	case strings.TrimSpace(reason) == "":
		return errors.New("the reason is required (one line: what was measured)")
	case len(reason) > maxConflictReason || strings.ContainsFunc(reason, func(r rune) bool { return r < 0x20 || r == 0x7f }):
		return fmt.Errorf("the reason must be one line of at most %d bytes with no control characters", maxConflictReason)
	}
	return nil
}

// AddConflict adds a rule; one that is there already (same class and the same two patterns, either order) changes nothing. The
// message says which.
func (g *Gate) AddConflict(class Class, a, b, reason string) (msg string, added bool, err error) {
	if err := ValidateConflict(class, a, b, reason); err != nil {
		return "", false, err
	}
	by := g.RankBy()
	msg, err = g.editConflicts(func(rules []Conflict) ([]Conflict, bool, string) {
		for i, r := range rules {
			if samePair(r, class, a, b) {
				return rules, false, "already there, nothing written: " + ConflictLine(i+1, r)
			}
		}
		c := Conflict{Class: class, A: strings.Join(patternWords(a), " "), B: strings.Join(patternWords(b), " "), Reason: strings.TrimSpace(reason), By: by, At: time.Now().UTC()}
		added = true
		return append(rules, c), true, fmt.Sprintf("added %s; %d rule(s) now", ConflictLine(len(rules)+1, c), len(rules)+1)
	})
	return msg, added && err == nil, err
}

// RemoveConflictAt removes the rule at a 1-based index of the list.
func (g *Gate) RemoveConflictAt(n int) (msg string, removed bool, err error) {
	msg, err = g.editConflicts(func(rules []Conflict) ([]Conflict, bool, string) {
		if n < 1 || n > len(rules) {
			return rules, false, fmt.Sprintf("no such rule: #%d (there are %d)", n, len(rules))
		}
		gone := rules[n-1]
		out := append(append([]Conflict(nil), rules[:n-1]...), rules[n:]...)
		removed = true
		return out, true, fmt.Sprintf("removed %s; %d rule(s) now", ConflictLine(n, gone), len(out))
	})
	return msg, removed && err == nil, err
}

// RemoveConflictPair removes the rule with this class and these two patterns (either order).
func (g *Gate) RemoveConflictPair(class Class, a, b string) (msg string, removed bool, err error) {
	msg, err = g.editConflicts(func(rules []Conflict) ([]Conflict, bool, string) {
		for i, r := range rules {
			if samePair(r, class, a, b) {
				out := append(append([]Conflict(nil), rules[:i]...), rules[i+1:]...)
				removed = true
				return out, true, fmt.Sprintf("removed %s; %d rule(s) now", ConflictLine(i+1, r), len(out))
			}
		}
		return rules, false, fmt.Sprintf("no such rule: %s: %s × %s", class, strings.Join(patternWords(a), " "), strings.Join(patternWords(b), " "))
	})
	return msg, removed && err == nil, err
}

// Conflicts lists the rules of the gate directory and, when the file cannot be used, why.
func (g *Gate) Conflicts() ([]Conflict, string) { return LoadConflicts(g.Config().Dir) }
