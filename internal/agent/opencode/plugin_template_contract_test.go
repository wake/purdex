package opencode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// payloadFixturesDir is the relative path from the package directory to
// the per-event payload fixture set. The directory name is versioned to
// the audited upstream tag so future audits never overwrite historical
// fixtures (plan v1.3 §2.4).
const payloadFixturesDir = "testdata/opencode-1.14.23-payloads"

// fixtureEnvelope is the JSON shape of every payload fixture in
// payloadFixturesDir. Kind discriminates Bus events (use EventType +
// Properties) from strong hooks (use HookName + Input + Output) so OC1
// and OC1a can dispatch through a single loader.
type fixtureEnvelope struct {
	Kind       string                 `json:"kind"`
	EventType  string                 `json:"eventType,omitempty"`
	HookName   string                 `json:"hookName,omitempty"`
	Properties map[string]interface{} `json:"properties,omitempty"`
	Input      map[string]interface{} `json:"input,omitempty"`
	Output     map[string]interface{} `json:"output,omitempty"`
}

func loadPayloadFixture(t *testing.T, name string) fixtureEnvelope {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(payloadFixturesDir, name))
	if err != nil {
		t.Fatalf("load fixture %q: %v", name, err)
	}
	var env fixtureEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("parse fixture %q: %v", name, err)
	}
	return env
}

// pluginSimState is a Go-side simulator for the JS callbacks rendered by
// renderManagedPlugin. It mirrors the production JS at the payload-shape
// level — intentionally distinct from the simpler pluginState used by
// the legacy state-machine tests (which suppresses idle via a single
// bool rather than a sessionID set). Production NEVER calls this; OC1
// uses it to verify the JS template would emit a given (Name, Payload)
// for a given fixture.
type pluginSimState struct {
	activeSubagents        map[string]string
	suppressIdleForSession map[string]bool
}

func newPluginSimState() *pluginSimState {
	return &pluginSimState{
		activeSubagents:        make(map[string]string),
		suppressIdleForSession: make(map[string]bool),
	}
}

// simulateBusEvent dispatches a Bus event payload through the equivalent
// of renderManagedPlugin's `event` switch.
func (s *pluginSimState) simulateBusEvent(eventType string, properties map[string]any) (mappedHookEvent, bool) {
	switch eventType {
	case "session.created":
		return mappedHookEvent{
			Name: "SessionStart",
			Payload: map[string]any{
				"session_id": strMapVal(properties, "sessionID"),
			},
		}, true
	case "permission.asked":
		return mappedHookEvent{
			Name: "PermissionRequest",
			Payload: map[string]any{
				"request_type": "permission",
				"permission":   properties["permission"],
				"patterns":     properties["patterns"],
			},
		}, true
	case "question.asked":
		return mappedHookEvent{
			Name: "PermissionRequest",
			Payload: map[string]any{
				"request_type": "question",
				"questions":    properties["questions"],
			},
		}, true
	case "session.error":
		sessionID := strMapVal(properties, "sessionID")
		if sessionID != "" {
			s.suppressIdleForSession[sessionID] = true
		}
		var errName, errDetails string
		if errObj, ok := properties["error"].(map[string]any); ok {
			errName = strMapVal(errObj, "name")
			if dataObj, ok := errObj["data"].(map[string]any); ok {
				errDetails = strMapVal(dataObj, "message")
			}
		}
		return mappedHookEvent{
			Name: "StopFailure",
			Payload: map[string]any{
				"error":         errName,
				"error_details": errDetails,
			},
		}, true
	case "session.status":
		// Decision 3 switch: subscribe session.status, filter to {type:"idle"}.
		// Decision 4 defer: busy/retry variants no-op; trigger conditions
		// for adoption are tracked in follow-up issue #661.
		statusObj, _ := properties["status"].(map[string]any)
		if strMapVal(statusObj, "type") != "idle" {
			return mappedHookEvent{}, false
		}
		sessionID := strMapVal(properties, "sessionID")
		if s.suppressIdleForSession[sessionID] {
			delete(s.suppressIdleForSession, sessionID)
			return mappedHookEvent{}, false
		}
		return mappedHookEvent{
			Name: "Stop",
			Payload: map[string]any{
				"session_id": sessionID,
			},
		}, true
	case "session.deleted":
		return mappedHookEvent{
			Name: "SessionEnd",
			Payload: map[string]any{
				"session_id": strMapVal(properties, "sessionID"),
			},
		}, true
	}
	return mappedHookEvent{}, false
}

// simulateStrongHook dispatches a strong-hook fixture through the
// equivalent of renderManagedPlugin's top-level callback for that hook.
func (s *pluginSimState) simulateStrongHook(hookName string, input, output map[string]any) (mappedHookEvent, bool) {
	switch hookName {
	case "chat.message":
		return s.simulateChatMessage(input, output), true
	case "tool.execute.before":
		return s.simulateToolExecuteBefore(input, output)
	case "tool.execute.after":
		return s.simulateToolExecuteAfter(input, output)
	}
	return mappedHookEvent{}, false
}

func (s *pluginSimState) simulateChatMessage(input, output map[string]any) mappedHookEvent {
	sessionID := strMapVal(input, "sessionID")
	messageID := strMapVal(input, "messageID")
	if messageID == "" {
		if msg, ok := output["message"].(map[string]any); ok {
			messageID = strMapVal(msg, "id")
		}
	}
	var agentName string
	if msg, ok := output["message"].(map[string]any); ok {
		agentName = strMapVal(msg, "agent")
	}
	if agentName == "" {
		agentName = strMapVal(input, "agent")
	}
	var modelName string
	if model, ok := input["model"].(map[string]any); ok {
		provID := strMapVal(model, "providerID")
		modelID := strMapVal(model, "modelID")
		modelName = provID + "/" + modelID
	}
	return mappedHookEvent{
		Name: "UserPromptSubmit",
		Payload: map[string]any{
			"session_id": sessionID,
			"message_id": messageID,
			"agent":      agentName,
			"modelName":  modelName,
			"source":     "chat.message",
		},
	}
}

func (s *pluginSimState) simulateToolExecuteBefore(input, output map[string]any) (mappedHookEvent, bool) {
	if strMapVal(input, "tool") != "task" || strMapVal(input, "callID") == "" {
		return mappedHookEvent{}, false
	}
	sessionID := strMapVal(input, "sessionID")
	callID := strMapVal(input, "callID")
	key := sessionID + ":" + callID
	if _, exists := s.activeSubagents[key]; exists {
		return mappedHookEvent{}, false
	}
	args, _ := output["args"].(map[string]any)
	agentType := simAgentTypeFromArgs(args)
	s.activeSubagents[key] = agentType
	return mappedHookEvent{
		Name: "SubagentStart",
		Payload: map[string]any{
			"agent_id":    callID,
			"agent_type":  agentType,
			"description": strMapVal(args, "description"),
			"prompt":      strMapVal(args, "prompt"),
		},
	}, true
}

func (s *pluginSimState) simulateToolExecuteAfter(input, output map[string]any) (mappedHookEvent, bool) {
	if strMapVal(input, "tool") != "task" || strMapVal(input, "callID") == "" {
		return mappedHookEvent{}, false
	}
	sessionID := strMapVal(input, "sessionID")
	callID := strMapVal(input, "callID")
	key := sessionID + ":" + callID
	agentType, exists := s.activeSubagents[key]
	if !exists {
		return mappedHookEvent{}, false
	}
	delete(s.activeSubagents, key)
	return mappedHookEvent{
		Name: "SubagentStop",
		Payload: map[string]any{
			"agent_id":   callID,
			"agent_type": agentType,
			"title":      strMapVal(output, "title"),
			"output":     strMapVal(output, "output"),
		},
	}, true
}

func simAgentTypeFromArgs(args map[string]any) string {
	if t := strMapVal(args, "subagent_type"); t != "" {
		return t
	}
	if t := strMapVal(args, "agent"); t != "" {
		return t
	}
	return "task"
}

// pathMustExist resolves a dotted path through nested map[string]any and
// returns ok=false if any segment is missing or types mismatch. OC1a uses
// it to assert the fixture covers every field the JS plugin reads.
func pathMustExist(envelope map[string]any, dotted string) (any, bool) {
	parts := strings.Split(dotted, ".")
	var cur any = envelope
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, exists := m[p]
		if !exists {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// envelopeAsMap exposes the fixtureEnvelope as a generic map[string]any
// so pathMustExist walks Bus events and strong hooks the same way.
func envelopeAsMap(env fixtureEnvelope) map[string]any {
	switch env.Kind {
	case "bus-event":
		return map[string]any{"properties": map[string]any(env.Properties)}
	case "strong-hook":
		return map[string]any{
			"input":  map[string]any(env.Input),
			"output": map[string]any(env.Output),
		}
	}
	return nil
}

// pluginEventContract declares what every plugin-subscribed event needs
// in its fixture. RequiredFields is the minimum surface OC1a guarantees;
// extra fields in fixtures are fine.
type pluginEventContract struct {
	Event          string
	FixtureFile    string
	Kind           string
	RequiredFields []string
}

// pluginContracts is the post-Commit-5 event-to-handler map. The
// `session.status` entry is the Decision 3 switch target (filtered to
// {type:"idle"} inside the simulator); pre-switch sessions used a bare
// `session.idle` Bus event, deprecated upstream.
func pluginContracts() []pluginEventContract {
	return []pluginEventContract{
		{
			Event:          "session.created",
			FixtureFile:    "session.created.json",
			Kind:           "bus-event",
			RequiredFields: []string{"properties.sessionID"},
		},
		{
			Event:          "permission.asked",
			FixtureFile:    "permission.asked.json",
			Kind:           "bus-event",
			RequiredFields: []string{"properties.permission", "properties.patterns"},
		},
		{
			Event:          "question.asked",
			FixtureFile:    "question.asked.json",
			Kind:           "bus-event",
			RequiredFields: []string{"properties.questions"},
		},
		{
			Event:       "session.error",
			FixtureFile: "session.error.json",
			Kind:        "bus-event",
			RequiredFields: []string{
				"properties.sessionID",
				"properties.error.name",
				"properties.error.data.message",
			},
		},
		{
			Event:          "session.status",
			FixtureFile:    "session.status.json",
			Kind:           "bus-event",
			RequiredFields: []string{"properties.sessionID", "properties.status.type"},
		},
		{
			Event:          "session.deleted",
			FixtureFile:    "session.deleted.json",
			Kind:           "bus-event",
			RequiredFields: []string{"properties.sessionID"},
		},
		{
			Event:       "chat.message",
			FixtureFile: "chat.message.json",
			Kind:        "strong-hook",
			RequiredFields: []string{
				"input.sessionID",
				"input.messageID",
				"input.agent",
				"input.model.providerID",
				"input.model.modelID",
				"output.message.id",
				"output.message.agent",
			},
		},
		{
			Event:       "tool.execute.before",
			FixtureFile: "tool.execute.before.json",
			Kind:        "strong-hook",
			RequiredFields: []string{
				"input.tool",
				"input.callID",
				"input.sessionID",
				"output.args.subagent_type",
				"output.args.description",
				"output.args.prompt",
			},
		},
		{
			Event:       "tool.execute.after",
			FixtureFile: "tool.execute.after.json",
			Kind:        "strong-hook",
			RequiredFields: []string{
				"input.tool",
				"input.callID",
				"input.sessionID",
				"output.title",
				"output.output",
			},
		},
	}
}

// TestOpenCodeTemplateEventContractsDocumented (plan v1.3 §3 OC1a). For
// every plugin-subscribed event the JS plugin reads from properties /
// input / output, the corresponding fixture must declare a field at the
// same dotted path. This is the bidirectional contract guard between
// renderManagedPlugin and the testdata/opencode-1.14.23-payloads/ set.
func TestOpenCodeTemplateEventContractsDocumented(t *testing.T) {
	for _, c := range pluginContracts() {
		c := c
		t.Run(c.Event, func(t *testing.T) {
			env := loadPayloadFixture(t, c.FixtureFile)
			if env.Kind != c.Kind {
				t.Fatalf("fixture kind = %q, want %q", env.Kind, c.Kind)
			}
			switch c.Kind {
			case "bus-event":
				if env.EventType != c.Event {
					t.Fatalf("fixture eventType = %q, want %q", env.EventType, c.Event)
				}
			case "strong-hook":
				if env.HookName != c.Event {
					t.Fatalf("fixture hookName = %q, want %q", env.HookName, c.Event)
				}
			}
			payload := envelopeAsMap(env)
			for _, p := range c.RequiredFields {
				v, ok := pathMustExist(payload, p)
				if !ok {
					t.Errorf("required field %q missing from fixture %q", p, c.FixtureFile)
					continue
				}
				if isZeroValue(v) {
					t.Errorf("required field %q is zero/empty in fixture %q", p, c.FixtureFile)
				}
			}
		})
	}
}

// isZeroValue is a permissive zero check for fixture-loaded values: empty
// string / nil / empty slice / empty map all count. Numbers and bools are
// never zero (false / 0 may be valid payload values).
func isZeroValue(v any) bool {
	if v == nil {
		return true
	}
	switch x := v.(type) {
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

// TestOpenCodePluginTemplate_UsesVerifiedEvents (plan v1.3 §3 OC1). For
// each plugin-subscribed event, load the fixture, run the JS-mirrored
// simulator, and assert the (Name, Payload) the JS plugin would emit().
// Unconditional (Round 1 C1).
//
// Verifies the post-Decision-3 mapping where `session.status` filtered to
// {type:"idle"} drives Stop; busy/retry variants are received-but-no-op
// (Decision 4 defer).
func TestOpenCodePluginTemplate_UsesVerifiedEvents(t *testing.T) {
	cases := []struct {
		event         string
		fixtureFile   string
		kind          string
		setup         func(*pluginSimState, fixtureEnvelope)
		expectName    string
		expectPayload map[string]any
	}{
		{
			event:       "session.created",
			fixtureFile: "session.created.json",
			kind:        "bus-event",
			expectName:  "SessionStart",
			expectPayload: map[string]any{
				"session_id": "ses_fixture_session_created_001",
			},
		},
		{
			event:       "permission.asked",
			fixtureFile: "permission.asked.json",
			kind:        "bus-event",
			expectName:  "PermissionRequest",
			expectPayload: map[string]any{
				"request_type": "permission",
				"permission":   "edit",
				"patterns":     []any{"src/**/*.ts"},
			},
		},
		{
			event:       "question.asked",
			fixtureFile: "question.asked.json",
			kind:        "bus-event",
			expectName:  "PermissionRequest",
			expectPayload: map[string]any{
				"request_type": "question",
				"questions": []any{
					map[string]any{"label": "approve?", "value": "approve"},
					map[string]any{"label": "deny?", "value": "deny"},
				},
			},
		},
		{
			event:       "session.error",
			fixtureFile: "session.error.json",
			kind:        "bus-event",
			expectName:  "StopFailure",
			expectPayload: map[string]any{
				"error":         "ProviderError",
				"error_details": "request timed out",
			},
		},
		{
			event:       "session.status",
			fixtureFile: "session.status.json",
			kind:        "bus-event",
			expectName:  "Stop",
			expectPayload: map[string]any{
				"session_id": "ses_fixture_status_idle_001",
			},
		},
		{
			event:       "session.deleted",
			fixtureFile: "session.deleted.json",
			kind:        "bus-event",
			expectName:  "SessionEnd",
			expectPayload: map[string]any{
				"session_id": "ses_fixture_deleted_001",
			},
		},
		{
			event:       "chat.message",
			fixtureFile: "chat.message.json",
			kind:        "strong-hook",
			expectName:  "UserPromptSubmit",
			expectPayload: map[string]any{
				"session_id": "ses_fixture_chat_001",
				"message_id": "msg_fixture_chat_001",
				"agent":      "build",
				"modelName":  "anthropic/claude-3-5-sonnet",
				"source":     "chat.message",
			},
		},
		{
			event:       "tool.execute.before",
			fixtureFile: "tool.execute.before.json",
			kind:        "strong-hook",
			expectName:  "SubagentStart",
			expectPayload: map[string]any{
				"agent_id":    "call_fixture_task_001",
				"agent_type":  "Explore",
				"description": "explore process tree",
				"prompt":      "find tmux sessions",
			},
		},
		{
			event:       "tool.execute.after",
			fixtureFile: "tool.execute.after.json",
			kind:        "strong-hook",
			setup: func(s *pluginSimState, env fixtureEnvelope) {
				key := strMapVal(env.Input, "sessionID") + ":" + strMapVal(env.Input, "callID")
				s.activeSubagents[key] = "Explore"
			},
			expectName: "SubagentStop",
			expectPayload: map[string]any{
				"agent_id":   "call_fixture_task_001",
				"agent_type": "Explore",
				"title":      "explore complete",
				"output":     "found 3 sessions",
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.event, func(t *testing.T) {
			env := loadPayloadFixture(t, tc.fixtureFile)
			state := newPluginSimState()
			if tc.setup != nil {
				tc.setup(state, env)
			}
			var (
				got mappedHookEvent
				ok  bool
			)
			switch tc.kind {
			case "bus-event":
				got, ok = state.simulateBusEvent(env.EventType, env.Properties)
			case "strong-hook":
				got, ok = state.simulateStrongHook(env.HookName, env.Input, env.Output)
			default:
				t.Fatalf("unknown kind %q", tc.kind)
			}
			if !ok {
				t.Fatalf("simulator did not emit for %q", tc.event)
			}
			if got.Name != tc.expectName {
				t.Fatalf("event name = %q, want %q", got.Name, tc.expectName)
			}
			if !reflect.DeepEqual(got.Payload, tc.expectPayload) {
				t.Fatalf("payload mismatch for %q\n  got:  %s\n  want: %s",
					tc.event, prettyJSON(got.Payload), prettyJSON(tc.expectPayload))
			}
		})
	}
}

func prettyJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%#v", v)
	}
	return string(b)
}
