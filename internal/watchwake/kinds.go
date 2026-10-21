package watchwake

// Kind is one kind of wwd alert as the App's checklist shows it: the wire spelling, the levels the alert can come in (a kind can climb: orphans
// goes from the digest to a push), and its name in Chinese (the same words the wake note uses).
type Kind struct {
	Kind   string   `json:"kind"`
	Levels []string `json:"levels"`
	Label  string   `json:"label"`
}

// levelOrder is the order a kind's levels are listed in.
var levelOrder = []string{"digest", "push", "watch"}

// KnownKinds is every kind the advisor raises (internal/wwd/wire/alert.go), in a fixed order. The labels are the ones in
// internal/module/wwdalerts/wake.go (a test there keeps the two the same); the levels are the ones the rules in internal/wwd/advisor give
// the alerts they raise.
var KnownKinds = []Kind{
	{"disk_low", []string{"push"}, "磁碟空間不足"},
	{"disk_falling", []string{"digest"}, "磁碟空間快速下降"},
	{"pressure_held", []string{"push"}, "記憶體壓力持續"},
	{"mserv_service", []string{"push"}, "mserv 服務異常"},
	{"worker_crash_loop", []string{"push"}, "worker 反覆當機"},
	{"silence", []string{"push"}, "主機失聯"},
	{"thrash", []string{"digest"}, "換頁過量"},
	{"resident_growth", []string{"digest"}, "常駐記憶體成長"},
	{"orphans", []string{"digest", "push"}, "孤兒行程"},
	{"pressure_watch", []string{"watch"}, "記憶體壓力偏高"},
	{"pressure_danger", []string{"push"}, "記憶體壓力危險"},
	{"swap_growth", []string{"watch"}, "swap 持續增加"},
	{"queue_stalled", []string{"watch"}, "佇列停滯"},
	{"job_requeued", []string{"watch"}, "job 反覆被放回"},
	{"queue_backlog", []string{"watch"}, "佇列積壓"},
	{"queue_unclaimable", []string{"watch"}, "沒有主機接得了"},
}

// KindLabel is the Chinese name of a known kind.
func KindLabel(kind string) (string, bool) {
	for _, k := range KnownKinds {
		if k.Kind == kind {
			return k.Label, true
		}
	}
	return "", false
}
