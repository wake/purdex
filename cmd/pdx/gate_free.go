package main

// `pdx gate free <class>-<k>` (host-load-control spec §12.5, plan W4): end the processes that hold a slot's lock. It lists them, refuses
// a slot whose gate is alive unless --force (that command is running as it should: interrupt it instead), asks for "y" (anything else
// aborts; --yes skips the question), sends SIGTERM, SIGKILL only to those still alive after 5 s, and says whether the slot is free.

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/redact"
)

// gateFreeEnv and gateStdin are the host and the answer's source; a test replaces them.
var (
	gateFreeEnv           = gate.DefaultFreeEnv
	gateStdin   io.Reader = os.Stdin
)

func gateFree(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gate free", flag.ContinueOnError)
	fs.SetOutput(stderr)
	yes := fs.Bool("yes", false, "do not ask")
	force := fs.Bool("force", false, "also when the gate of the slot is alive")
	cfgPath := fs.String("config", "", "config.toml")
	words, err := parseMixed(fs, args)
	if err != nil {
		return 2
	}
	if len(words) != 1 {
		fmt.Fprint(stderr, "usage: pdx gate free <heavy|light>-<number> [--yes] [--force] [--config <path>]\n")
		return 2
	}
	class, k, err := gate.ParseSlot(words[0])
	if err != nil {
		fmt.Fprintf(stderr, "pdx gate free: %v\n", err)
		return 2
	}
	cfg := loadGateConfig(*cfgPath, stderr)
	env := gateFreeEnv()
	tg, err := gate.PlanFree(cfg, env, class, k)
	if err != nil {
		if err == gate.ErrSlotFree {
			fmt.Fprintf(stdout, "%s-%d 沒有被佔用，不需要做什麼\n", class, k)
			return 0
		}
		fmt.Fprintf(stderr, "pdx gate free: %v\n", err)
		return 1
	}
	h := tg.Holder
	fmt.Fprintf(stdout, "%s-%d\n", class, k)
	if len(h.Argv) > 0 {
		fmt.Fprintf(stdout, "  指令  %s\n", sanitizeCell(strings.Join(redact.Argv(h.Argv, 120), " ")))
	}
	if h.Cwd != "" {
		fmt.Fprintf(stdout, "  目錄  %s\n", sanitizeCell(h.Cwd))
	}
	if !h.Started.IsZero() {
		fmt.Fprintf(stdout, "  開始  %s（%s 前）\n", h.Started.Local().Format("01-02 15:04:05"), time.Since(h.Started).Round(time.Second))
	}
	if h.Session != "" {
		fmt.Fprintf(stdout, "  session  %s\n", sanitizeCell(h.Session))
	}
	switch {
	case tg.GateAlive:
		fmt.Fprintf(stdout, "  gate  pid %d 還活著\n", h.PID)
	case h.PID > 0:
		fmt.Fprintf(stdout, "  gate  pid %d 已不在\n", h.PID)
	}
	for _, s := range tg.Locked {
		fmt.Fprintf(stdout, "  開著鎖檔的行程  pid %d  %s\n", s.PID, sanitizeCell(s.Cmd))
	}
	for _, s := range tg.Skipped {
		fmt.Fprintf(stdout, "  不會結束  pid %d  %s（只是繼承了鎖檔；結束它會連帶結束別的東西）\n", s.PID, sanitizeCell(s.Cmd))
	}
	if tg.GateAlive && !*force {
		fmt.Fprintf(stderr, "pdx gate free: 這個名額的 gate（pid %d）還活著，那條指令正在正常執行；請中斷那條指令，真的要結束它加 --force\n", h.PID)
		return 1
	}
	if len(tg.Locked) == 0 {
		fmt.Fprintln(stderr, "pdx gate free: 找不到可以結束的行程（鎖也許剛好放開了，或握著的只有 shell／tmux 這類不會被結束的行程；用 pdx gate status 再看一次）")
		return 1
	}
	if !*yes {
		fmt.Fprintln(stdout, "（lsof 只看得出哪些行程開著鎖檔，分不出誰真的握著鎖；請確認上面都是該結束的）")
		fmt.Fprint(stdout, "結束上面這些行程？(y/N) ")
		line, _ := bufio.NewReader(gateStdin).ReadString('\n')
		if strings.TrimSpace(line) != "y" {
			fmt.Fprintln(stdout, "\n已取消")
			return 1
		}
	}
	res, err := gate.Free(cfg, env, class, k, tg)
	if err != nil {
		fmt.Fprintf(stderr, "pdx gate free: %v；沒有結束任何行程，請再看一次（pdx gate status）\n", err)
		return 1
	}
	if len(res.Termed) > 0 {
		fmt.Fprintf(stdout, "已送 SIGTERM：%v\n", res.Termed)
	}
	if len(res.Killed) > 0 {
		fmt.Fprintf(stdout, "5 秒後仍在，已送 SIGKILL：%v\n", res.Killed)
	}
	if res.Free {
		fmt.Fprintf(stdout, "%s-%d 已空出\n", class, k)
		return 0
	}
	fmt.Fprintf(stderr, "pdx gate free: %s-%d 還沒空出", class, k)
	for _, s := range res.Left {
		fmt.Fprintf(stderr, "；還被 pid %d（%s）佔著", s.PID, sanitizeCell(s.Cmd))
	}
	fmt.Fprintln(stderr)
	return 1
}
