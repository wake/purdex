package main

// The human half of `pdx watch wake-settings show`: the document, where it came from, how the patrol has been doing. Moved out of
// watch_wake_settings.go unchanged.

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// printWakeView is `show`: the document as a person reads it, then where it came from and how it has been working.
func printWakeView(w io.Writer, v wakeView) {
	var s struct {
		Enabled   bool `json:"enabled"`
		AutoStart bool `json:"auto_start"`
		Rules     struct {
			Patrol struct {
				Prompt      string         `json:"prompt"`
				IntervalMin map[string]int `json:"interval_min"`
				MaxGapHours int            `json:"max_gap_hours"`
				Times       []string       `json:"times"`
				TZ          string         `json:"tz"`
			} `json:"patrol"`
			DiskLow struct {
				Enabled bool   `json:"enabled"`
				Prompt  string `json:"prompt"`
				BelowGB int    `json:"below_gb"`
			} `json:"disk_low"`
			WwdAlert struct {
				Enabled  bool     `json:"enabled"`
				Prompt   string   `json:"prompt"`
				Levels   []string `json:"levels"`
				KindsOff []string `json:"kinds_off"`
			} `json:"wwd_alert"`
			Unattended struct {
				Enabled bool   `json:"enabled"`
				Prompt  string `json:"prompt"`
			} `json:"unattended_changed"`
			UserBack struct {
				Enabled bool   `json:"enabled"`
				Prompt  string `json:"prompt"`
				AwayMin int    `json:"away_min"`
			} `json:"user_back"`
		} `json:"rules"`
	}
	_ = json.Unmarshal(v.Settings, &s)
	role := "不是正本"
	if v.IsSource {
		role = "這台就是正本"
	}
	fmt.Fprintf(w, "喚醒設定  正本：%s（%s）  revision %d  etag %s\n", sanitizeCell(v.Source), role, v.Revision, sanitizeCell(v.Etag))
	fetched := "從未取到"
	if v.FetchedAt != nil {
		fetched = sanitizeCell(*v.FetchedAt)
	}
	fmt.Fprintf(w, "  stale：%s   唯讀：%s   取得時間：%s\n", wakeYesNo(v.Stale), wakeYesNo(v.Readonly), fetched)
	fmt.Fprintf(w, "  總開關 %s（關掉時巡檢以外的自動叫醒都不發生，不影響巡檢）   自動叫起 %s（同上）\n", wakeOnOff(s.Enabled), wakeOnOff(s.AutoStart))
	p, iv := s.Rules.Patrol, s.Rules.Patrol.IntervalMin
	fmt.Fprintf(w, "巡檢（不能關）\n  間隔（分鐘，0＝只剩上限）：休息 %d  在電腦前 %d  不在電腦前 %d  吃緊 %d  平穩 %d\n", iv["resting"], iv["present"], iv["away"], iv["busy"], iv["calm"])
	times := "（無）"
	if len(p.Times) > 0 {
		times = sanitizeCell(strings.Join(p.Times, " "))
	}
	fmt.Fprintf(w, "  最長不巡上限 %d 小時   固定時刻 %s   時區 %s\n  prompt：%s\n", p.MaxGapHours, times, sanitizeCell(p.TZ), wakePromptText(p.Prompt))
	fmt.Fprintf(w, "disk_low  %s  低於 %d GB 叫一次\n  prompt：%s\n", wakeOnOff(s.Rules.DiskLow.Enabled), s.Rules.DiskLow.BelowGB, wakePromptText(s.Rules.DiskLow.Prompt))
	a := s.Rules.WwdAlert
	fmt.Fprintf(w, "wwd_alert  %s  levels %s  不叫的 kind %s\n  prompt：%s\n", wakeOnOff(a.Enabled), wakeListText(a.Levels), wakeListText(a.KindsOff), wakePromptText(a.Prompt))
	fmt.Fprintf(w, "unattended_changed  %s\n  prompt：%s\n", wakeOnOff(s.Rules.Unattended.Enabled), wakePromptText(s.Rules.Unattended.Prompt))
	fmt.Fprintf(w, "user_back  %s  離開 %d 分鐘以上回來才叫\n  prompt：%s\n", wakeOnOff(s.Rules.UserBack.Enabled), s.Rules.UserBack.AwayMin, wakePromptText(s.Rules.UserBack.Prompt))
	ps := v.PatrolStatus
	fmt.Fprintf(w, "巡檢狀態\n  最近成功：%s\n", wakeWhenWhere(ps.LastOKAt, ps.LastOKHost))
	if ps.LastFailAt != nil {
		fmt.Fprintf(w, "  最近失敗：%s  %s  %s\n", sanitizeCell(*ps.LastFailAt), sanitizeCell(wakeDeref(ps.LastFailCode)), sanitizeCell(wakeDeref(ps.LastFailDetail)))
	} else {
		fmt.Fprintln(w, "  最近失敗：（無）")
	}
	if len(v.Suppressed) == 0 {
		fmt.Fprintln(w, "最近因設定而沒叫的：（無）")
		return
	}
	fmt.Fprintf(w, "最近因設定而沒叫的（%d 筆，顯示最近 %d）：\n", len(v.Suppressed), min(len(v.Suppressed), wakeSuppressedN))
	for i, x := range v.Suppressed {
		if i == wakeSuppressedN {
			break
		}
		fmt.Fprintf(w, "  %s  %s  %s  %s  %s\n", sanitizeCell(x.At), sanitizeCell(x.Host), sanitizeCell(x.Reason), sanitizeCell(x.Why), sanitizeCell(x.Detail))
	}
}

func wakeYesNo(b bool) string {
	if b {
		return "是"
	}
	return "否"
}

func wakeOnOff(b bool) string {
	if b {
		return "開"
	}
	return "關"
}

func wakePromptText(s string) string {
	if s == "" {
		return "（空）"
	}
	return sanitizeCell(s)
}

func wakeListText(l []string) string {
	if len(l) == 0 {
		return "（無）"
	}
	return sanitizeCell(strings.Join(l, ","))
}

func wakeDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func wakeWhenWhere(at, host *string) string {
	if at == nil {
		return "（無）"
	}
	if host != nil && *host != "" {
		return sanitizeCell(*at) + "  " + sanitizeCell(*host)
	}
	return sanitizeCell(*at)
}
