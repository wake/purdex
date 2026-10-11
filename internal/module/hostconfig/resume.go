package hostconfig

import "maps"

// ResumeTemplatesKey is the registry key of the module as a ResumeTemplateReader (no-agent launch panel spec §2.2).
const ResumeTemplatesKey = "hostconfig.resume-templates"

// DefaultResumeTemplates are the shapes that shipped hardcoded: a host with no overrides resumes exactly these. They
// mirror the SPA's DEFAULT_RESUME_TEMPLATES (spa/src/lib/resume-templates.ts); resume_test.go reads that file and fails
// when the two differ.
var DefaultResumeTemplates = map[string]ResumeTemplatePair{
	"cc":       {Exact: "claude --resume {id}", Fallback: "claude -c"},
	"codex":    {Exact: "codex resume {id}", Fallback: "codex resume --last"},
	"opencode": {Exact: "opencode -s {id}", Fallback: "opencode -c"},
}

// ResumeTemplateReader is what the session module type-asserts on the registry value: the host's effective resume
// templates by agent type. The session module never opens host_config.db itself.
type ResumeTemplateReader interface {
	ResumeTemplates() (map[string]ResumeTemplatePair, error)
}

// ResumeTemplates answers the templates the GET shows and the SPA's lookup resolves: the defaults, with each agent the
// owner overrode replaced WHOLE by their pair (an empty field stays empty — it is the owner's, and the caller reports
// it) and agents the defaults do not know added. The stored row is read as leniently as the GET reads it
// (readResumeTemplates): a pair that does not validate is left out so that agent keeps its default, and a value that
// is not an object at all is no overrides — a damaged row never fails a launch plan. The map is the caller's own.
func (m *Module) ResumeTemplates() (map[string]ResumeTemplatePair, error) {
	out := maps.Clone(DefaultResumeTemplates)
	e, err := m.store.Get(KeyResumeTemplates)
	if err != nil {
		return nil, err
	}
	if e.Value == nil {
		return out, nil
	}
	if over, ok := readResumeTemplates(e.Value).items.(map[string]ResumeTemplatePair); ok {
		maps.Copy(out, over)
	}
	return out, nil
}
