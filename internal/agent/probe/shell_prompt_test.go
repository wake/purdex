package probe

import "testing"

func TestShellPrompt_Patterns(t *testing.T) {
	cases := []string{
		"user@host % ",
		"/tmp/project $ ",
		"root # ",
		"worktree > ",
	}
	for _, content := range cases {
		if !looksLikeShellPrompt(content) {
			t.Fatalf("looksLikeShellPrompt(%q) = false", content)
		}
	}
}

func TestShellPrompt_NotMarkdownCodeBlock(t *testing.T) {
	content := "```bash\n$ echo hi\n```"
	if !looksLikeShellPrompt(content) {
		t.Fatal("markdown code fence should still look like shell prompt; caller must combine with pid liveness")
	}
}
