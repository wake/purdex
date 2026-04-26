package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wake/purdex/internal/agent"
)

const codexHooksSupportedVersion = "0.121.0"

func (p *Provider) InstallHooks(pdxPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	return mergeCodexHooks(hooksPath, pdxPath, false)
}

func (p *Provider) RemoveHooks(pdxPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	return mergeCodexHooks(hooksPath, pdxPath, true)
}

func (p *Provider) CheckHooks() (agent.HookStatus, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return agent.HookStatus{Issues: []string{"cannot find home dir"}}, err
	}
	agentVersion := agent.DetectHookAgentVersion("codex", "--version")
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	data, err := os.ReadFile(hooksPath)
	if err != nil {
		return agent.HookStatus{
			Installed:        false,
			Events:           map[string]agent.HookEventInfo{},
			Issues:           []string{"hooks.json not found"},
			AgentVersion:     agentVersion,
			SupportedVersion: codexHooksSupportedVersion,
			ExceedsSupport:   agent.CompareHookAgentVersions(agentVersion, codexHooksSupportedVersion) > 0,
		}, nil
	}
	var hooksFile map[string]any
	if err := json.Unmarshal(data, &hooksFile); err != nil {
		return agent.HookStatus{}, fmt.Errorf("parse hooks.json: %w", err)
	}
	hooks, _ := hooksFile["hooks"].(map[string]any)
	allSpecs := p.Events()
	specs := make([]agent.HookEventSpec, 0, len(allSpecs))
	for _, spec := range allSpecs {
		if agent.IsInstallableHookSpec(spec) {
			specs = append(specs, spec)
		}
	}
	events := make(map[string]agent.HookEventInfo, len(specs))
	var issues []string
	var upgrades []string
	allInstalled := true
	for _, spec := range specs {
		info, addIssues, blocks := checkCodexEvent(spec, hooks)
		// Propagate the FutureOnly bit to HookEventInfo so the UI can
		// render "tolerated absent" vs "required missing" distinctly.
		info.FutureOnly = spec.FutureOnly
		events[spec.Name] = info
		issues = append(issues, addIssues...)
		if blocks {
			allInstalled = false
		}
		if spec.FutureOnly && !info.Installed {
			// Tolerated-absent FutureOnly → advertise as available upgrade.
			if _, keyExists := hooks[spec.Name]; !keyExists {
				upgrades = append(upgrades, spec.Name)
			}
		}
	}
	managed := codexHooksManaged(hooks, allSpecs)
	return agent.HookStatus{
		Installed:         allInstalled,
		Managed:           managed,
		UpgradesAvailable: upgrades,
		Events:            events,
		Issues:            issues,
		AgentVersion:      agentVersion,
		SupportedVersion:  codexHooksSupportedVersion,
		ExceedsSupport:    agent.CompareHookAgentVersions(agentVersion, codexHooksSupportedVersion) > 0,
	}, nil
}

// codexHooksManaged reports whether hooks.json contains any pdx-owned
// entry (per-event matcher-group shape OR legacy direct-entry shape).
// Distinct from CheckHooks.Installed: a drifted-but-pdx-owned state has
// Managed=true and Installed=false, which the UI needs so the Remove
// button stays enabled.
func codexHooksManaged(hooks map[string]any, specs []agent.HookEventSpec) bool {
	for _, spec := range specs {
		entries, ok := hooks[spec.Name]
		if !ok {
			continue
		}
		if hasLegacyPdxDirectCodexEntry(entries) {
			return true
		}
		for _, groupEntry := range codexMatcherGroups(entries) {
			group, ok := groupEntry.(map[string]any)
			if !ok {
				continue
			}
			for _, entry := range toCodexEntrySlice(group["hooks"]) {
				m, ok := entry.(map[string]any)
				if !ok {
					continue
				}
				cmd, _ := m["command"].(string)
				if isPdxCommandCodexOwned(cmd) {
					return true
				}
			}
		}
	}
	return false
}

// checkCodexEvent classifies a single hook event as absent / broken / valid
// per fix-plan §1.4 and applies the FutureOnly-aware decision table. It
// takes the full hooks map so absent vs present-but-empty can be told apart
// via the double-return form `entries, ok := hooks[spec.Name]` — a hand-edited
// value of `[]any{}` must be classified as broken, not absent.
//
// Returns:
//   - HookEventInfo for events[spec.Name]
//   - zero or more Issue strings to append
//   - whether this event blocks allInstalled
func checkCodexEvent(spec agent.HookEventSpec, hooks map[string]any) (agent.HookEventInfo, []string, bool) {
	entries, keyExists := hooks[spec.Name]
	// State A — absent (strict: key not present in map).
	if !keyExists {
		info := agent.HookEventInfo{Installed: false}
		if spec.FutureOnly {
			// Tolerated legacy: no issue, does not block.
			return info, nil, false
		}
		return info, []string{spec.Name + " hook not installed"}, true
	}
	// Legacy direct-entry shape is its own signal (present but uses the
	// pre-0.121 shape pdx installer no longer writes).
	if hasLegacyPdxDirectCodexEntry(entries) {
		return agent.HookEventInfo{Installed: false},
			[]string{spec.Name + " hook uses legacy format; reinstall required"},
			true
	}
	// Find a pdx command inside the matcher-group list that passes strict
	// per-event validation (tokenized binary / --agent codex / matching event
	// name tail). Empty entries, wrong shape, wrong agent, wrong event-name
	// tail all collapse to command == "" here → State B. Substring matches
	// alone no longer qualify: PR #616 review Finding #1.
	command := findPdxCommandInCodexForEvent(entries, spec.Name)
	if command == "" {
		if spec.FutureOnly {
			return agent.HookEventInfo{Installed: false},
				[]string{spec.Name + " hook: pdx command malformed (FutureOnly event has existing hook entry but pdx path incorrect — run install to repair)"},
				true
		}
		return agent.HookEventInfo{Installed: false},
			[]string{spec.Name + " hook: pdx command not found"},
			true
	}
	// State C — valid.
	return agent.HookEventInfo{Installed: true, Command: command}, nil, false
}

func mergeCodexHooks(path, pdxPath string, remove bool) error {
	hooksFile := make(map[string]any)
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &hooksFile); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var hooks map[string]any
	if h, ok := hooksFile["hooks"]; ok {
		hooks, _ = h.(map[string]any)
	}
	if hooks == nil {
		hooks = make(map[string]any)
	}
	for _, spec := range codexEventSpecs {
		installable := agent.IsInstallableHookSpec(spec)
		if !remove && !installable {
			continue
		}
		event := spec.Name
		if remove && !installable {
			existing, ok := hooks[event]
			if !ok {
				continue
			}
			entries := filterOutPdxCodexKnownEvents(existing)
			if len(entries) == 0 {
				delete(hooks, event)
			} else {
				hooks[event] = entries
			}
			continue
		}
		entries := filterOutPdxCodexKnownEvents(hooks[event])
		if !remove {
			entries = append(entries, map[string]any{
				"hooks": []any{
					map[string]any{
						"type":    "command",
						"command": fmt.Sprintf(`"%s" hook --agent codex %s`, pdxPath, event),
						"timeout": 5,
					},
				},
			})
		}
		if remove && len(entries) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = entries
		}
	}
	hooksFile["hooks"] = hooks
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	out, err := json.MarshalIndent(hooksFile, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, out, 0644); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// isPdxCommandCodex is the relaxed shape check used by filter / legacy
// detection paths where we only need to know "does this look like any pdx
// hook command?" — e.g. when mergeCodexHooks strips all pdx entries before
// rewriting, or when hasLegacyPdxDirectCodexEntry tags the pre-0.121
// direct-entry shape. Per-event health assertions must not use this; see
// isPdxCommandCodexForEvent (PR #616 review Finding #1).
func isPdxCommandCodex(cmd string) bool {
	// Match both quoted ("/path/pdx" hook) and unquoted (/path/pdx hook) forms.
	normalized := strings.ReplaceAll(cmd, `"`, "")
	return strings.Contains(normalized, "pdx hook")
}

// isPdxCommandCodexForEvent reports whether cmd is a well-formed pdx hook
// command for the given event name. Rules (all must hold):
//  1. The first non-empty token, stripped of surrounding quotes, has
//     basename "pdx" (covers "/abs/path/pdx", "pdx", the quoted forms
//     mergeCodexHooks writes, and paths containing spaces such as
//     "/Applications/Purdex Beta/pdx").
//  2. Some later token equals "hook".
//  3. Two consecutive tokens "--agent" "codex" appear after "hook".
//  4. The final non-empty token equals eventName exactly.
//
// A non-pdx third-party command, a pdx command targeting a different
// agent, or a copy of a pdx command whose event-name tail does not
// match the key it is filed under, all return false. Token splitting
// is quote-aware (see tokenizeCodexCommand) so double-quoted paths
// with literal spaces round-trip correctly — the installer emits
// "<pdxPath>" hook --agent codex <event>, where pdxPath may contain
// spaces on macOS app-bundle layouts.
func isPdxCommandCodexForEvent(cmd string, eventName string) bool {
	tokens := tokenizeCodexCommand(cmd)
	if len(tokens) == 0 {
		return false
	}
	first := tokens[0]
	if first == "" {
		return false
	}
	if filepath.Base(first) != "pdx" {
		return false
	}
	hasHook := false
	hasAgentCodex := false
	for i := 1; i < len(tokens); i++ {
		if tokens[i] == "hook" {
			hasHook = true
		}
		if tokens[i] == "--agent" && i+1 < len(tokens) && tokens[i+1] == "codex" {
			hasAgentCodex = true
		}
	}
	if !hasHook || !hasAgentCodex {
		return false
	}
	return tokens[len(tokens)-1] == eventName
}

// tokenizeCodexCommand splits a codex hook command string into tokens,
// respecting single and double quotes. Whitespace inside a quoted run
// is preserved; quote characters themselves are stripped. This lets
// the installer's output "<pdxPath>" hook --agent codex <event>
// round-trip correctly when pdxPath contains spaces (e.g. macOS
// "/Applications/Purdex Beta/pdx"). Escape sequences are not
// interpreted — the installer never emits them.
func tokenizeCodexCommand(cmd string) []string {
	var tokens []string
	var cur strings.Builder
	inQuote := false
	var quoteChar byte
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if inQuote {
			if c == quoteChar {
				inQuote = false
				quoteChar = 0
				continue
			}
			cur.WriteByte(c)
			continue
		}
		if c == '"' || c == '\'' {
			inQuote = true
			quoteChar = c
			continue
		}
		if c == ' ' || c == '\t' || c == '\n' {
			flush()
			continue
		}
		cur.WriteByte(c)
	}
	flush()
	return tokens
}

// findPdxCommandInCodexForEvent walks the matcher-group list and returns the
// first command that passes isPdxCommandCodexForEvent for the given event
// name. Empty return means no qualifying command was found → checkCodexEvent
// classifies the event as broken.
func findPdxCommandInCodexForEvent(entries any, eventName string) string {
	for _, groupEntry := range codexMatcherGroups(entries) {
		group, ok := groupEntry.(map[string]any)
		if !ok {
			continue
		}
		for _, entry := range toCodexEntrySlice(group["hooks"]) {
			m, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := m["command"].(string)
			if isPdxCommandCodexForEvent(cmd, eventName) {
				return cmd
			}
		}
	}
	return ""
}

func toCodexEntrySlice(v any) []any {
	if v == nil {
		return []any{}
	}
	if arr, ok := v.([]any); ok {
		return arr
	}
	return []any{}
}

func codexMatcherGroups(v any) []any {
	var groups []any
	for _, entry := range toCodexEntrySlice(v) {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if _, ok := m["hooks"]; ok {
			groups = append(groups, m)
		}
	}
	return groups
}

// hasLegacyPdxDirectCodexEntry reports true only when a pre-0.121 direct-entry
// shape ({"type": "command", "command": "...pdx hook..."}) is found. Presence
// of a third-party legacy entry alongside a correctly-installed pdx matcher
// group is not a pdx reinstall trigger and must not report false.
func hasLegacyPdxDirectCodexEntry(v any) bool {
	for _, entry := range toCodexEntrySlice(v) {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if _, hasHooks := m["hooks"]; hasHooks {
			continue
		}
		if _, hasType := m["type"]; !hasType {
			continue
		}
		cmd, _ := m["command"].(string)
		if isPdxCommandCodex(cmd) {
			return true
		}
	}
	return false
}

func cloneCodexMap(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func filterOutPdxCodex(entries any) []any {
	return filterOutPdxCodexWithPredicate(entries, isPdxCommandCodex)
}

func filterOutPdxCodexKnownEvents(entries any) []any {
	return filterOutPdxCodexWithPredicate(entries, isPdxCommandCodexOwned)
}

func filterOutPdxCodexWithPredicate(entries any, isOwned func(string) bool) []any {
	var result []any
	for _, entry := range toCodexEntrySlice(entries) {
		group, ok := entry.(map[string]any)
		if !ok {
			result = append(result, entry)
			continue
		}
		if _, ok := group["hooks"]; !ok {
			if _, ok := group["type"]; ok {
				cmd, _ := group["command"].(string)
				if isOwned(cmd) {
					continue
				}
				result = append(result, map[string]any{
					"hooks": []any{cloneCodexMap(group)},
				})
				continue
			}
			result = append(result, entry)
			continue
		}
		var kept []any
		for _, hookEntry := range toCodexEntrySlice(group["hooks"]) {
			m, ok := hookEntry.(map[string]any)
			if !ok {
				kept = append(kept, hookEntry)
				continue
			}
			cmd, _ := m["command"].(string)
			if !isOwned(cmd) {
				kept = append(kept, hookEntry)
			}
		}
		if len(kept) == 0 {
			continue
		}
		cloned := cloneCodexMap(group)
		cloned["hooks"] = kept
		result = append(result, cloned)
	}
	return result
}

func isPdxCommandCodexKnownEvent(cmd string, known map[string]bool) bool {
	return isPdxCommandCodexOwned(cmd) && known[lastCodexCommandToken(cmd)]
}

func isPdxCommandCodexOwned(cmd string) bool {
	tokens := tokenizeCodexCommand(cmd)
	if len(tokens) == 0 || filepath.Base(tokens[0]) != "pdx" {
		return false
	}
	hasHook := false
	hasAgentCodex := false
	for i := 1; i < len(tokens); i++ {
		if tokens[i] == "hook" {
			hasHook = true
		}
		if tokens[i] == "--agent" && i+1 < len(tokens) && tokens[i+1] == "codex" {
			hasAgentCodex = true
		}
	}
	return hasHook && hasAgentCodex
}

func lastCodexCommandToken(cmd string) string {
	tokens := tokenizeCodexCommand(cmd)
	if len(tokens) == 0 {
		return ""
	}
	return tokens[len(tokens)-1]
}

func codexKnownEventNames() map[string]bool {
	known := make(map[string]bool, len(codexEventSpecs))
	for _, spec := range codexEventSpecs {
		known[spec.Name] = true
	}
	return known
}
