package agent

import "testing"

// G3c: LightGeneration moves for every hook event and every mod event the module takes in (the conversation stream compares it
// between polls), and stays put while nothing comes in.
func TestLightGeneration_MovesForHookAndModEventsOnly(t *testing.T) {
	m, _ := nonTmuxModule(t)
	g0 := m.LightGeneration()
	if m.LightGeneration() != g0 {
		t.Fatal("moved with nothing coming in")
	}
	postEvent(m, `{"tmux_session":"","tmux_pane_id":"",`+nonTmuxTail+`,"raw_event":{"session_id":"abc-123"}}`)
	g1 := m.LightGeneration()
	if g1 <= g0 {
		t.Fatalf("a hook event did not move the generation: %d -> %d", g0, g1)
	}
	feedMod(m, "stream-1", modEv("abc-123", "turn.start", `{}`))
	if g2 := m.LightGeneration(); g2 <= g1 {
		t.Fatalf("a mod event did not move the generation: %d -> %d", g1, g2)
	}
	var nilModule *Module
	if nilModule.LightGeneration() != 0 {
		t.Fatal("a nil module has generation 0")
	}
}
