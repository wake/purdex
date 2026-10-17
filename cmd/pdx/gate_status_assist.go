package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/wake/purdex/internal/gate"
)

// printAssistStatus adds the assist hosts to `pdx gate status`: for each, the runs out there and the holds, then the commands whose
// ticket is out. Nothing is printed on a host with no assist host.
func printAssistStatus(w io.Writer, st gate.Status) {
	for _, h := range st.Assist {
		state := "關"
		if h.On {
			state = "開"
		}
		fmt.Fprintf(w, "輔測 %s（要求：%s）%d/%d 在外面跑\n", sanitizeCell(h.Host), state, len(h.Held), h.Size)
		for _, r := range h.Held {
			age := ""
			if !r.Started.IsZero() {
				age = time.Since(r.Started).Round(time.Second).String()
			}
			fmt.Fprintf(w, "  %s-%d  pid %d  %s  %s\n", r.Class, r.Slot, r.PID, age, sanitizeCell(strings.Join(r.Argv, " ")))
		}
		for _, hd := range h.Holds {
			fmt.Fprintf(w, "  暫停 %s 到 %s：%s\n", sanitizeCell(hd.Repo), hd.Until.Local().Format("15:04:05"), sanitizeCell(hd.Why))
		}
	}
	for _, list := range [][]gate.OutWaiter{st.OutHeavy, st.OutLight} {
		for _, o := range list {
			fmt.Fprintf(w, "  外送 %s  pid %d  → %s  %s\n", o.Class, o.PID, sanitizeCell(o.Host), sanitizeCell(strings.Join(o.Argv, " ")))
		}
	}
}
