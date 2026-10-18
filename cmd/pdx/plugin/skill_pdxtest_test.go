package plugin

import (
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/wwd/testcmd"
)

// section returns the text of the "## <heading>" section of a skill, up to the next "## ".
func section(t *testing.T, doc, heading string) string {
	t.Helper()
	_, rest, ok := strings.Cut(doc, "\n## "+heading+"\n")
	if !ok {
		t.Fatalf("no section %q", heading)
	}
	body, _, _ := strings.Cut(rest, "\n## ")
	return body
}

// The pdx-test skill names the commands and exit codes pdx test really has, and keeps its warnings: do not get around the wait, do
// not resend a job whose wait was only interrupted, and keep the outer gate of a script (the contract of pdx-gate).
func TestSkill_TestSaysWhatAnAgentNeeds(t *testing.T) {
	b, err := fs.ReadFile(Files(), "skills/pdx-test/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		"name: pdx-test", ".wwd/tasks.toml",
		"pdx test --list", "pdx test --adhoc -- ", "不經 shell",
		"pdx jobs show <id>", "pdx jobs log <id>", "pdx jobs cancel <id>",
		"`pdx-gate`", "兩者並存",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("SKILL.md of pdx-test lacks %q", want)
		}
	}
	wait := section(t, s, "等待")
	for _, want := range []string{"不要重跑、不要自己加 `&`、`nohup`", "Ctrl-C 的意義看工作在哪", "pdx jobs cancel <id>"} {
		if !strings.Contains(wait, want) {
			t.Errorf("the waiting section lacks %q", want)
		}
	}
	// every exit code pdx test has, with its meaning in the same row
	rows := map[int]string{}
	for _, line := range strings.Split(section(t, s, "結束碼"), "\n") {
		cells := strings.Split(line, "|")
		if len(cells) < 4 {
			continue
		}
		var code int
		if _, err := fmt.Sscanf(strings.TrimSpace(cells[1]), "%d", &code); err == nil {
			rows[code] = cells[2] + cells[3]
		}
	}
	for _, c := range []struct {
		code int
		name string
		must []string // words that tell this row is about this code
	}{
		{testcmd.ExitOK, "ExitOK", []string{"成功"}},
		{testcmd.ExitFailed, "ExitFailed", []string{"失敗", "lost"}},
		{testcmd.ExitNoWWD, "ExitNoWWD", []string{"wwd 連不上"}},
		{testcmd.ExitUsage, "ExitUsage", []string{"manifest", "參數錯誤"}},
		{testcmd.ExitNoHost, "ExitNoHost", []string{"沒有任何主機", "標籤"}},
		{testcmd.ExitExpired, "ExitExpired", []string{"佇列", "沒有任何東西跑過"}},
		{testcmd.ExitTimeout, "ExitTimeout", []string{"超過時間"}},
		// 130 is a cancelled job and also a wait that was only interrupted while the job goes on: never resend blindly
		{testcmd.ExitCancelled, "ExitCancelled", []string{"工作已取消", "等待被中斷，但工作本身還在跑", "先 `pdx jobs show <id>` 看終態再決定", "不要重送"}},
	} {
		row, ok := rows[c.code]
		if !ok {
			t.Errorf("SKILL.md of pdx-test has no row for %s (%d)", c.name, c.code)
			continue
		}
		for _, w := range c.must {
			if !strings.Contains(row, w) {
				t.Errorf("the row of %s (%d) lacks %q: %s", c.name, c.code, w, row)
			}
		}
	}
	// the gate: nested under a script's gate, the claim of pdx-gate (one slot for the whole script) holds
	rel := section(t, s, "與 pdx-gate 的關係")
	for _, want := range []string{"照 `pdx-gate` 的規矩保留外層 gate", "沿用它而不重排", "heavy 名額底下的 light 指令", "light 名額底下的 heavy 指令"} {
		if !strings.Contains(rel, want) {
			t.Errorf("the pdx-gate section lacks %q", want)
		}
	}
	if strings.Contains(s, "雙重排隊") {
		t.Error("the skill says a nested gate queues twice; gate.nested makes the inner command use the outer slot")
	}
	// the flags it tells an agent to use exist in the usage text
	for _, flag := range []string{"--list", "--adhoc", "--detach", "--class", "--json"} {
		if !strings.Contains(testcmd.Usage, flag) {
			t.Errorf("pdx test no longer has %s, which the skill names", flag)
		}
	}
}
