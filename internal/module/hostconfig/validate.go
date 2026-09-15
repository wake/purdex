package hostconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	maxItems        = 200
	maxResumeAgents = 32
	nameMaxRunes    = 64
	pathMaxBytes    = 1024
	commandMaxBytes = 4096
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

func checkIDs(seen map[string]bool, id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("invalid id %q", id)
	}
	if seen[id] {
		return fmt.Errorf("duplicate id %q", id)
	}
	seen[id] = true
	return nil
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
		if err := checkIDs(ids, p.ID); err != nil {
			return nil, err
		}
		name, err := validName("project name", p.Name)
		if err != nil {
			return nil, err
		}
		if !slugPattern.MatchString(p.Slug) {
			return nil, fmt.Errorf("invalid slug %q", p.Slug)
		}
		if slugs[p.Slug] {
			return nil, fmt.Errorf("duplicate slug %q", p.Slug)
		}
		slugs[p.Slug] = true
		path := strings.TrimSpace(p.Path)
		if path == "" || len(path) > pathMaxBytes || strings.ContainsRune(path, 0) {
			return nil, fmt.Errorf("invalid path for project %q", p.ID)
		}
		if !(strings.HasPrefix(path, "/") || path == "~" || strings.HasPrefix(path, "~/")) {
			return nil, fmt.Errorf("path must be absolute or start with ~/ (project %q)", p.ID)
		}
		out = append(out, Project{ID: p.ID, Name: name, Slug: p.Slug, Path: path})
	}
	return out, nil
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
		if err := checkIDs(ids, c.ID); err != nil {
			return nil, err
		}
		name, err := validName("command name", c.Name)
		if err != nil {
			return nil, err
		}
		if c.Command == "" || len(c.Command) > commandMaxBytes || strings.ContainsRune(c.Command, 0) {
			return nil, fmt.Errorf("invalid command for %q", c.ID)
		}
		switch c.Icon.Kind {
		case "agent":
			if !agentIconValues[c.Icon.Value] {
				return nil, fmt.Errorf("invalid agent icon %q", c.Icon.Value)
			}
		case "phosphor":
			if !phosphorPattern.MatchString(c.Icon.Value) {
				return nil, fmt.Errorf("invalid phosphor icon %q", c.Icon.Value)
			}
		default:
			return nil, fmt.Errorf("invalid icon kind %q", c.Icon.Kind)
		}
		out = append(out, Command{ID: c.ID, Name: name, Command: c.Command, Icon: c.Icon})
	}
	return out, nil
}

func validTemplate(v string) bool {
	return len(v) <= commandMaxBytes && !strings.ContainsRune(v, 0)
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
		if !agentTypePattern.MatchString(agent) {
			return nil, fmt.Errorf("invalid agent type %q", agent)
		}
		if !validTemplate(pair.Exact) || !validTemplate(pair.Fallback) {
			return nil, fmt.Errorf("invalid template for %q", agent)
		}
		out[agent] = pair
	}
	return out, nil
}
