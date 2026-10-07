package nex

import (
	"testing"

	pstore "github.com/wake/purdex/internal/store"
)

// The registry name sits after the Nexen title and before the first prompt.
func TestConversationTitle_RegistryNameOrder(t *testing.T) {
	cases := []struct {
		name                      string
		custom, ai, nexen, prompt string
		registry                  string
		title, source             string
	}{
		{name: "name, no prompt", registry: "purdex-47", title: "purdex-47", source: "registry"},
		{name: "name beats prompt", registry: "purdex-47", prompt: "fix it", title: "purdex-47", source: "registry"},
		{name: "ai beats name", ai: "AI", registry: "purdex-47", title: "AI", source: "ai"},
		{name: "custom beats name", custom: "C", registry: "purdex-47", title: "C", source: "custom"},
		{name: "nexen beats name", nexen: "N", registry: "purdex-47", title: "N", source: "nexen"},
		{name: "blank name skipped", registry: "  \t", prompt: "fix it", title: "fix it", source: "prompt"},
		{name: "no name, no prompt", title: cvA[:8], source: "session_id"},
		{name: "no name, prompt", prompt: "fix it", title: "fix it", source: "prompt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := pstore.ConversationIndexRow{CustomTitle: tc.custom, AITitle: tc.ai, FirstPrompt: tc.prompt}
			title, source := conversationTitle(cvA, row, tc.nexen, tc.registry)
			if title != tc.title || source != tc.source {
				t.Fatalf("got %q (%s), want %q (%s)", title, source, tc.title, tc.source)
			}
		})
	}
}

// buildConversations feeds in.Names (sid -> registry name) into the title.
func TestConversations_RegistryNameFromInputs(t *testing.T) {
	w := newCVWorld().index(cvIndexRow(cvA, "cli", "cli")).list(cvA, 2000)
	w.in.Names = map[string]string{cvA: "purdex-47"}
	got := cvOne(t, w.build().Ended)
	if got.Title != "purdex-47" || got.TitleSource != "registry" {
		t.Fatalf("title = %q (%s)", got.Title, got.TitleSource)
	}
}
