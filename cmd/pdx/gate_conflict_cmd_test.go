package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- pdx gate conflict (#3058, spec §5.3) ----

func conflictCmd(t *testing.T, cfg string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := gateMain(append(append([]string{"conflict"}, args...), "--config", cfg), &out, &errb)
	return code, out.String(), errb.String()
}

func conflictFile(t *testing.T, cfg string) string {
	t.Helper()
	return filepath.Join(filepath.Dir(cfg), "gate", "conflicts.json")
}

// Add, list, remove by index and by pair; an existing pair (either order) changes nothing and says so; a missing one says so.
// Mutation gates: no dedupe → two rules (red); the pair order respected → "already there" missing (red); the index off by one → red.
func TestGateConflict_AddListRemove(t *testing.T) {
	cfg, _ := gateEnv(t, noAdmission)
	t.Setenv("PDX_SESSION_ID", "")
	code, out, _ := conflictCmd(t, cfg)
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "no conflict rules")

	code, out, errb := conflictCmd(t, cfg, "add", "--class", "heavy", "tsc -b", "vitest related", "--reason", "thrash 10-16 22:34")
	require.Equal(t, 0, code, errb)
	assert.Contains(t, out, "added #1 heavy: tsc -b × vitest related (thrash 10-16 22:34)")
	assert.Contains(t, out, "1 rule(s) now")

	code, out, _ = conflictCmd(t, cfg, "add", "--class", "heavy", "vitest related", "tsc -b", "--reason", "same pair reversed")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "already there, nothing written")
	code, out, _ = conflictCmd(t, cfg, "add", "--class", "heavy", "go vet", "go vet", "--reason", "no two vets")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "added #2")
	code, out, _ = conflictCmd(t, cfg, "add", "--class", "light", "tsc -b", "vitest related", "--reason", "another class is another rule")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "added #3")

	_, out, _ = conflictCmd(t, cfg)
	assert.Equal(t, 3, strings.Count(strings.TrimSpace(out), "\n")+1)
	_, out, _ = conflictCmd(t, cfg, "--json")
	var doc struct {
		Rules []struct {
			Class, A, B, Reason string
		}
		Error string
	}
	require.NoError(t, json.Unmarshal([]byte(out), &doc))
	require.Len(t, doc.Rules, 3)
	assert.Equal(t, "go vet", doc.Rules[1].A)

	code, out, _ = conflictCmd(t, cfg, "remove", "2")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "removed #2 heavy: go vet × go vet")
	code, out, _ = conflictCmd(t, cfg, "remove", "--class", "light", "vitest related", "tsc -b")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "removed #2 light")
	code, out, _ = conflictCmd(t, cfg, "remove", "9")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "no such rule: #9 (there are 1)")
	code, out, _ = conflictCmd(t, cfg, "remove", "0")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "no such rule: #0 (there are 1)")
	code, out, _ = conflictCmd(t, cfg, "remove", "2")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "no such rule: #2 (there are 1)", "one past the end is not a rule")
	code, out, _ = conflictCmd(t, cfg, "remove", "--class", "heavy", "nope", "never")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "no such rule: heavy: nope × never")
	_, out, _ = conflictCmd(t, cfg)
	assert.Contains(t, out, "tsc -b × vitest related")
	assert.NotContains(t, out, "go vet")
	code, out, _ = conflictCmd(t, cfg, "remove", "1")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "removed #1 heavy: tsc -b × vitest related", "the last rule goes by its index too")
	assert.Contains(t, out, "0 rule(s) now")
}

// The refusals: missing reason, empty pattern, a bad class, a reason that is not one short line, wrong shapes.
func TestGateConflict_Refusals(t *testing.T) {
	cfg, _ := gateEnv(t, noAdmission)
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"no reason":            {[]string{"add", "--class", "heavy", "a b", "c d"}, "usage"},
		"empty reason":         {[]string{"add", "--class", "heavy", "a b", "c d", "--reason", " "}, "reason is required"},
		"two lines":            {[]string{"add", "--class", "heavy", "a b", "c d", "--reason", "x\ny"}, "one line"},
		"too long":             {[]string{"add", "--class", "heavy", "a b", "c d", "--reason", strings.Repeat("x", 201)}, "one line"},
		"empty pattern":        {[]string{"add", "--class", "heavy", " ", "c d", "--reason", "r"}, "tool word"},
		"bad class":            {[]string{"add", "--class", "huge", "a b", "c d", "--reason", "r"}, "heavy or light"},
		"no class":             {[]string{"add", "a b", "c d", "--reason", "r"}, "usage"},
		"one pattern":          {[]string{"add", "--class", "heavy", "a b", "--reason", "r"}, "usage"},
		"remove bad class":     {[]string{"remove", "--class", "huge", "a b", "c d"}, "heavy or light"},
		"remove empty pattern": {[]string{"remove", "--class", "heavy", " ", "c d"}, "tool word"},
		"remove nothing":       {[]string{"remove"}, "usage"},
		"remove word":          {[]string{"remove", "soon"}, "usage"},
		"stray words":          {[]string{"a b"}, "usage"},
	} {
		code, out, errb := conflictCmd(t, cfg, c.args...)
		assert.Equal(t, 2, code, name)
		assert.Contains(t, errb, c.want, name)
		assert.Empty(t, out, name)
	}
	_, err := os.Stat(conflictFile(t, cfg))
	assert.True(t, os.IsNotExist(err), "a refused edit writes nothing")
}

// A broken conflicts.json: the list says so and no rule is in force; an edit refuses to write over it.
func TestGateConflict_ABrokenFile(t *testing.T) {
	cfg, _ := gateEnv(t, noAdmission)
	require.NoError(t, os.MkdirAll(filepath.Dir(conflictFile(t, cfg)), 0o755))
	require.NoError(t, os.WriteFile(conflictFile(t, cfg), []byte("{nope"), 0o644))
	code, out, _ := conflictCmd(t, cfg)
	assert.Equal(t, 1, code)
	assert.Contains(t, out, "cannot be used")
	code, _, errb := conflictCmd(t, cfg, "add", "--class", "heavy", "a b", "c d", "--reason", "r")
	assert.Equal(t, 1, code)
	assert.Contains(t, errb, "cannot be read")
	b, _ := os.ReadFile(conflictFile(t, cfg))
	assert.Equal(t, "{nope", string(b), "the file was not written over")
	var st bytes.Buffer
	var e bytes.Buffer
	require.Equal(t, 0, gateMain([]string{"status", "--config", cfg}, &st, &e))
	assert.Contains(t, st.String(), "沒有規則生效")
}

// Two adds at once both land; an add and a remove of the same pair at once end in the order the lock was taken, and each prints what
// it found. Mutation gate: no lock → one add is lost (red, repeated).
func TestGateConflict_TwoEditsAtOnceLoseNothing(t *testing.T) {
	cfg, _ := gateEnv(t, noAdmission)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _, errb := conflictCmd(t, cfg, "add", "--class", "heavy", "tool-"+string(rune('a'+i)), "other", "--reason", "r")
			assert.Equal(t, 0, code, errb)
		}()
	}
	wg.Wait()
	_, out, _ := conflictCmd(t, cfg)
	assert.Equal(t, 8, strings.Count(strings.TrimSpace(out), "\n")+1, out)

	// add and remove of the same pair at once: whichever took the lock last leaves it
	for round := 0; round < 10; round++ {
		code, _, _ := conflictCmd(t, cfg, "remove", "--class", "heavy", "pair a", "pair b")
		assert.Equal(t, 0, code)
		var outs [2]string
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, outs[0], _ = conflictCmd(t, cfg, "add", "--class", "heavy", "pair a", "pair b", "--reason", "r")
		}()
		go func() {
			defer wg.Done()
			_, outs[1], _ = conflictCmd(t, cfg, "remove", "--class", "heavy", "pair a", "pair b")
		}()
		wg.Wait()
		_, list, _ := conflictCmd(t, cfg)
		there := strings.Contains(list, "pair a × pair b")
		if there {
			assert.Contains(t, outs[0], "added", "remove first (nothing to remove), then add")
			assert.Contains(t, outs[1], "no such rule")
		} else {
			assert.Contains(t, outs[0], "added", "add first, then remove")
			assert.Contains(t, outs[1], "removed")
		}
	}
}

// status: the rules, and per waiter what holds it (conflict_with / conflict_rule), in the text and in the JSON.
func TestGateConflict_StatusShowsTheRulesAndTheWaiterTheyHold(t *testing.T) {
	cfg, _ := gateEnv(t, "[gate.slots]\nheavy = 2\n"+noAdmission)
	code, _, errb := conflictCmd(t, cfg, "add", "--class", "heavy", "tool-a", "tool-b", "--reason", "they thrash")
	require.Equal(t, 0, code, errb)
	dir := t.TempDir()
	script := func(name string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\nwhile [ ! -e "+dir+"/go ]; do sleep 0.02; done\n"), 0o755))
		return p
	}
	a, b := script("tool-a"), script("tool-b")
	t.Cleanup(func() { _ = os.WriteFile(dir+"/go", nil, 0o644) })
	done := make(chan int, 2)
	for i, p := range []string{a, b} {
		go func() {
			var o, e bytes.Buffer
			done <- slotGateMain([]string{"--config", cfg, "--class", "heavy", "--fg", "--", p}, &o, &e)
		}()
		eventuallyOutput(t, func() bool {
			var o, e bytes.Buffer
			gateMain([]string{"status", "--config", cfg}, &o, &e)
			if i == 0 {
				// tool-a must hold its slot (not merely be queued) before tool-b arrives, or b waits on a's ticket.
				return strings.Contains(o.String(), "重 1/2 使用中") && strings.Contains(o.String(), filepath.Base(p))
			}
			return strings.Contains(o.String(), filepath.Base(p))
		})
	}
	var out, errb2 bytes.Buffer
	require.Equal(t, 0, gateMain([]string{"status", "--config", cfg}, &out, &errb2))
	assert.Contains(t, out.String(), "衝突規則 #1 heavy: tool-a × tool-b (they thrash)")
	assert.Contains(t, out.String(), "等 slot heavy-1：tool-a × tool-b（衝突規則：they thrash）")
	out.Reset()
	require.Equal(t, 0, gateMain([]string{"status", "--json", "--config", cfg}, &out, &errb2))
	var st struct {
		Conflicts  []map[string]any
		QueueHeavy []struct {
			ConflictWith   string `json:"conflict_with"`
			ConflictRule   string `json:"conflict_rule"`
			ConflictReason string `json:"conflict_reason"`
		}
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &st))
	require.Len(t, st.Conflicts, 1)
	require.Len(t, st.QueueHeavy, 1)
	assert.Equal(t, "slot heavy-1", st.QueueHeavy[0].ConflictWith)
	assert.Equal(t, "tool-a × tool-b", st.QueueHeavy[0].ConflictRule)
	assert.Equal(t, "they thrash", st.QueueHeavy[0].ConflictReason)
	require.NoError(t, os.WriteFile(dir+"/go", nil, 0o644))
	assert.Equal(t, 0, <-done)
	assert.Equal(t, 0, <-done)
}

// A rule whose reason is blank, too long or has a control character makes the file unusable, like a bad class: nothing is in force,
// the list says so, and an edit leaves the bytes alone. Mutation gate: LoadConflicts skips the reason check → red.
func TestGateConflict_ABadReasonInTheFile(t *testing.T) {
	for name, reason := range map[string]string{"blank": " ", "long": strings.Repeat("x", 201), "control": `a\u001b[31mb`} {
		cfg, _ := gateEnv(t, noAdmission)
		require.NoError(t, os.MkdirAll(filepath.Dir(conflictFile(t, cfg)), 0o755))
		doc := `{"rules":[{"class":"heavy","a":"tsc -b","b":"vitest related","reason":"` + reason + `"}]}`
		require.NoError(t, os.WriteFile(conflictFile(t, cfg), []byte(doc), 0o644))
		code, out, _ := conflictCmd(t, cfg)
		assert.Equal(t, 1, code, name)
		assert.Contains(t, out, "bad reason", name)
		code, _, errb := conflictCmd(t, cfg, "add", "--class", "heavy", "a b", "c d", "--reason", "r")
		assert.Equal(t, 1, code, name)
		assert.Contains(t, errb, "cannot be read", name)
		b, _ := os.ReadFile(conflictFile(t, cfg))
		assert.Equal(t, doc, string(b), name+": the file was not written over")
	}
}
