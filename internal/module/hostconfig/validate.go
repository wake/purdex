package hostconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	maxItems        = 200
	maxResumeAgents = 32
	nameMaxRunes    = 64
	pathMaxBytes    = 1024
	commandMaxBytes = 4096

	maxQuickReplies    = 20
	quickReplyMaxBytes = 1000
)

var (
	idPattern        = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	slugPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	phosphorPattern  = regexp.MustCompile(`^[A-Z][A-Za-z0-9]{0,63}$`)
	agentTypePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	agentIconValues  = map[string]bool{"cc-bot": true, "cc-star": true, "openai": true, "codex": true, "opencode": true}
)

// Project is a named working directory on this host.
type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
	Path string `json:"path"`
}

// CommandIcon identifies how a command is drawn: a built-in agent icon or a Phosphor icon name.
type CommandIcon struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// Command is a launchable shell command.
type Command struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Command string      `json:"command"`
	Icon    CommandIcon `json:"icon"`
}

// ResumeTemplatePair overrides one agent type's resume command templates.
type ResumeTemplatePair struct {
	Exact    string `json:"exact"`
	Fallback string `json:"fallback"`
}

func firstByte(raw []byte) byte {
	t := bytes.TrimLeft(raw, " \t\r\n")
	if len(t) == 0 {
		return 0
	}
	return t[0]
}

func decodeArray(raw json.RawMessage, v any) error {
	if firstByte(raw) != '[' || json.Unmarshal(raw, v) != nil {
		return errors.New("items must be a JSON array")
	}
	return nil
}

func validName(field, v string) (string, error) {
	t := strings.TrimSpace(v)
	n := utf8.RuneCountInString(t)
	if n == 0 {
		return "", fmt.Errorf("%s is required", field)
	}
	if n > nameMaxRunes {
		return "", fmt.Errorf("%s too long", field)
	}
	return t, nil
}

// checkID refuses an id off idPattern or already in seen. It records
// nothing: a row takes its id only once the whole row passes.
func checkID(seen map[string]bool, id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("invalid id %q", id)
	}
	if seen[id] {
		return fmt.Errorf("duplicate id %q", id)
	}
	return nil
}

// checkProject applies the PUT's rules to one project, in their order, and
// answers it as stored (name and path trimmed). Only a row that passes takes
// its id and slug, so a row the GET drops reserves nothing (#1889).
func checkProject(p Project, ids, slugs map[string]bool) (Project, error) {
	if err := checkID(ids, p.ID); err != nil {
		return Project{}, err
	}
	name, err := validName("project name", p.Name)
	if err != nil {
		return Project{}, err
	}
	if !slugPattern.MatchString(p.Slug) {
		return Project{}, fmt.Errorf("invalid slug %q", p.Slug)
	}
	if slugs[p.Slug] {
		return Project{}, fmt.Errorf("duplicate slug %q", p.Slug)
	}
	path := strings.TrimSpace(p.Path)
	if path == "" || len(path) > pathMaxBytes || strings.ContainsRune(path, 0) {
		return Project{}, fmt.Errorf("invalid path for project %q", p.ID)
	}
	if !(strings.HasPrefix(path, "/") || path == "~" || strings.HasPrefix(path, "~/")) {
		return Project{}, fmt.Errorf("path must be absolute or start with ~/ (project %q)", p.ID)
	}
	ids[p.ID], slugs[p.Slug] = true, true
	return Project{ID: p.ID, Name: name, Slug: p.Slug, Path: path}, nil
}

func normalizeProjects(raw json.RawMessage) ([]Project, error) {
	var in []Project
	if err := decodeArray(raw, &in); err != nil {
		return nil, err
	}
	if len(in) > maxItems {
		return nil, fmt.Errorf("at most %d projects", maxItems)
	}
	out := make([]Project, 0, len(in))
	ids, slugs := map[string]bool{}, map[string]bool{}
	for _, p := range in {
		row, err := checkProject(p, ids, slugs)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// readProjects is normalizeProjects' lenient twin (the GET's view).
func readProjects(raw json.RawMessage) readout {
	ids, slugs := map[string]bool{}, map[string]bool{}
	return readRows(raw, "project", "projects", maxItems, func(p Project) (Project, error) { return checkProject(p, ids, slugs) })
}

// checkCommand applies the PUT's rules to one command, in their order, and
// answers it as stored (name trimmed); only a row that passes takes its id.
func checkCommand(c Command, ids map[string]bool) (Command, error) {
	if err := checkID(ids, c.ID); err != nil {
		return Command{}, err
	}
	name, err := validName("command name", c.Name)
	if err != nil {
		return Command{}, err
	}
	if c.Command == "" || len(c.Command) > commandMaxBytes || strings.ContainsRune(c.Command, 0) {
		return Command{}, fmt.Errorf("invalid command for %q", c.ID)
	}
	switch c.Icon.Kind {
	case "agent":
		if !agentIconValues[c.Icon.Value] {
			return Command{}, fmt.Errorf("invalid agent icon %q", c.Icon.Value)
		}
	case "phosphor":
		if !phosphorPattern.MatchString(c.Icon.Value) {
			return Command{}, fmt.Errorf("invalid phosphor icon %q", c.Icon.Value)
		}
	default:
		return Command{}, fmt.Errorf("invalid icon kind %q", c.Icon.Kind)
	}
	ids[c.ID] = true
	return Command{ID: c.ID, Name: name, Command: c.Command, Icon: c.Icon}, nil
}

func normalizeCommands(raw json.RawMessage) ([]Command, error) {
	var in []Command
	if err := decodeArray(raw, &in); err != nil {
		return nil, err
	}
	if len(in) > maxItems {
		return nil, fmt.Errorf("at most %d commands", maxItems)
	}
	out := make([]Command, 0, len(in))
	ids := map[string]bool{}
	for _, c := range in {
		row, err := checkCommand(c, ids)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// readCommands is normalizeCommands' lenient twin (the GET's view).
func readCommands(raw json.RawMessage) readout {
	ids := map[string]bool{}
	return readRows(raw, "command", "commands", maxItems, func(c Command) (Command, error) { return checkCommand(c, ids) })
}

// QuickReply is one canned message a worker pane can send with one click.
type QuickReply struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// checkQuickReply applies the PUT's rules to one reply and answers it as
// stored (text trimmed); only a row that passes takes its id. encoding/json
// already replaces invalid UTF-8 with U+FFFD; the ValidString check is a
// guard should the decoding path ever change.
func checkQuickReply(q QuickReply, ids map[string]bool) (QuickReply, error) {
	if err := checkID(ids, q.ID); err != nil {
		return QuickReply{}, err
	}
	text := strings.TrimSpace(q.Text)
	if text == "" || len(text) > quickReplyMaxBytes || strings.ContainsRune(text, 0) || !utf8.ValidString(text) {
		return QuickReply{}, fmt.Errorf("invalid text for quick reply %q", q.ID)
	}
	ids[q.ID] = true
	return QuickReply{ID: q.ID, Text: text}, nil
}

// normalizeQuickReplies validates the list and stores each text trimmed.
func normalizeQuickReplies(raw json.RawMessage) ([]QuickReply, error) {
	var in []QuickReply
	if err := decodeArray(raw, &in); err != nil {
		return nil, err
	}
	if len(in) > maxQuickReplies {
		return nil, fmt.Errorf("at most %d quick replies", maxQuickReplies)
	}
	out := make([]QuickReply, 0, len(in))
	ids := map[string]bool{}
	for _, q := range in {
		row, err := checkQuickReply(q, ids)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// readQuickReplies is normalizeQuickReplies' lenient twin (the GET's view).
func readQuickReplies(raw json.RawMessage) readout {
	ids := map[string]bool{}
	return readRows(raw, "quick reply", "quick replies", maxQuickReplies, func(q QuickReply) (QuickReply, error) { return checkQuickReply(q, ids) })
}

func validTemplate(v string) bool {
	return len(v) <= commandMaxBytes && !strings.ContainsRune(v, 0)
}

// checkResumeTemplate applies the PUT's rules to one agent's entry.
func checkResumeTemplate(agent string, pair ResumeTemplatePair) error {
	if !agentTypePattern.MatchString(agent) {
		return fmt.Errorf("invalid agent type %q", agent)
	}
	if !validTemplate(pair.Exact) || !validTemplate(pair.Fallback) {
		return fmt.Errorf("invalid template for %q", agent)
	}
	return nil
}

func normalizeResumeTemplates(raw json.RawMessage) (map[string]ResumeTemplatePair, error) {
	var in map[string]ResumeTemplatePair
	if firstByte(raw) != '{' || json.Unmarshal(raw, &in) != nil {
		return nil, errors.New("items must be a JSON object")
	}
	if len(in) > maxResumeAgents {
		return nil, fmt.Errorf("at most %d agents", maxResumeAgents)
	}
	out := make(map[string]ResumeTemplatePair, len(in))
	for agent, pair := range in {
		if err := checkResumeTemplate(agent, pair); err != nil {
			return nil, err
		}
		out[agent] = pair
	}
	return out, nil
}

// readResumeTemplates is normalizeResumeTemplates' lenient twin (the GET's
// view): raw must be a JSON object, else it is invalid; each agent's entry
// is decoded on its own (a non-object or a field of the wrong type is "not
// a template pair") and checked like the PUT checks it. The keys go in
// sorted order, since Go's map order is random: past maxResumeAgents valid
// agents, the rest in that order are dropped, the same ones on every read.
func readResumeTemplates(raw json.RawMessage) readout {
	var in map[string]json.RawMessage
	if firstByte(raw) != '{' || json.Unmarshal(raw, &in) != nil {
		return readout{items: map[string]ResumeTemplatePair{}, invalid: errors.New("items must be a JSON object")}
	}
	var r readout
	out := make(map[string]ResumeTemplatePair, min(len(in), maxResumeAgents))
	for _, agent := range slices.Sorted(maps.Keys(in)) {
		if len(out) == maxResumeAgents {
			r.drop(fmt.Sprintf("at most %d agents; %q left out", maxResumeAgents, agent))
			continue
		}
		var pair ResumeTemplatePair
		if v := in[agent]; firstByte(v) != '{' || json.Unmarshal(v, &pair) != nil {
			r.drop(fmt.Sprintf("not a template pair for %q", agent))
			continue
		}
		if err := checkResumeTemplate(agent, pair); err != nil {
			r.drop(err.Error())
			continue
		}
		out[agent] = pair
	}
	r.items = out
	return r
}
