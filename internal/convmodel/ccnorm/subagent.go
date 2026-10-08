package ccnorm

import (
	"regexp"

	"github.com/wake/purdex/internal/convmodel"
)

// agentIDRe is what an agent id may look like: it names a file
// (`subagents/agent-<id>.jsonl`), so nothing that could walk a path gets in.
var agentIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// subagentOf links a task step to the agent it started: toolUseResult.agentId
// and isAsync, with the step's own description and subagent_type. nil when
// the result names no (valid) agent.
func (n *Normalizer) subagentOf(s *convmodel.Step, tur object) *convmodel.Subagent {
	id := tur.str("agentId")
	if id == "" {
		return nil
	}
	if !agentIDRe.MatchString(id) {
		n.skip("subagent:bad_id")
		return nil
	}
	in, _ := parseObject(s.Input)
	desc := in.str("description")
	if desc == "" {
		desc = tur.str("description")
	}
	typ := in.str("subagent_type")
	if typ == "" {
		typ = tur.str("subagent_type")
	}
	desc, _ = capText(desc, convmodel.MaxInputString)
	typ, _ = capText(typ, maxDenial)
	return &convmodel.Subagent{AgentID: id, Description: desc, Type: typ, Async: jsonTrue(tur.get("isAsync"))}
}
