package team

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Relay prompts (lead-team-relay spec §8.8, U21). The mod composes each of
// its three prompts as
//
//	fill(head, all) + fill(body, public) + ("\n" + fill(tail, all) unless tail is "")
//
// where the head and tail are RelayPromptFixedParts (the mod's own copy in
// cmd/pdx/plugin/purdex/hooks/prompts.js is generated from this file) and
// the body is the host's stored text or DefaultRelayPromptBodies. With the
// default bodies the write and seed prompts are the pre-P9a text byte for
// byte; the fix prompt differs only in "。\n" where it had "，" before
// 缺少段落 (plan v3, P9a deviation 3).

// RelayPromptMaxBytes is the longest body the daemon stores (U21 (d)).
const RelayPromptMaxBytes = 16 << 10

// RelayPromptVariables are the {{name}}s a body may use (U21 (d)); the mod
// fills them. The fixed parts also use {{op}}, {{nonce}} and {{missing}},
// which only the mod knows, and {{tasks}} (the seed's tail, T-2). P6-3a appends "git".
var RelayPromptVariables = []string{"path", "old_ref", "old_session", "context", "whoami"}

// RelayPromptBodies is one body per prompt: the stored values (where "" is
// unset) or the defaults.
type RelayPromptBodies struct {
	Write string `json:"write"`
	Fix   string `json:"fix"`
	Seed  string `json:"seed"`
}

// RelayPromptFixed is the part of one prompt before and after its body.
type RelayPromptFixed struct {
	Head string `json:"head"`
	Tail string `json:"tail"`
}

// RelayPromptSkeleton is the fixed parts of all three prompts.
type RelayPromptSkeleton struct {
	Write RelayPromptFixed `json:"write"`
	Fix   RelayPromptFixed `json:"fix"`
	Seed  RelayPromptFixed `json:"seed"`
}

// RelayPrompts is GET /api/relay/prompts and `pdx relay prompts`: the
// effective body of each prompt, the defaults (for 還原預設), and — beyond
// spec §8.8's list (plan v3 P9a deviation 1) — the fixed parts and the
// variables, so the settings page shows them from this one source.
type RelayPrompts struct {
	Write     string              `json:"write"`
	Fix       string              `json:"fix"`
	Seed      string              `json:"seed"`
	Defaults  RelayPromptBodies   `json:"defaults"`
	Fixed     RelayPromptSkeleton `json:"fixed"`
	Variables []string            `json:"variables"`
}

// lines joins prompt lines; the texts are not raw literals because they
// hold backticks.
func lines(l ...string) string { return strings.Join(l, "\n") }

// DefaultRelayPromptBodies are the built-in bodies, moved from register.js
// (spec §8.8): what an unset, empty or whitespace-only body means.
var DefaultRelayPromptBodies = RelayPromptBodies{
	Write: lines(
		"這個 session 的 context 已達接力門檻，使用者已核准接力（之後會 /clear）。",
		"請先停下手邊工作，用你完整的工具撰寫接力檔：{{path}}",
		"",
		"要求：",
		"- 自己跑 `git status`、`git diff --stat`、`git log --oneline -10` 取得檔案狀態，不要憑記憶寫。",
		"- 接力檔必須自成一體：讀它的是一個完全沒有這段對話記憶的新對話。",
	),
	Fix: "接力檔 {{path}} 不完整。",
	Seed: lines(
		"你是接手的新對話：前一段對話 context 已滿並已清空。",
		"請先讀接力檔 {{path}}，然後：",
		"1. 用三行複述：目標、下一步第一個動作、目前有哪些檔案異動。",
		"2. 跑 `git status` 確認與接力檔一致，不一致就指出來。",
		"3. 接著從「下一步」繼續原本的工作。",
		"回覆的第一行請寫「↪ 接手自 {{old_ref}}」。",
	),
}

// RelayPromptFixedParts are what no body can change (U21 (c)): the machine
// tag that opens the write and fix prompts and the seed's second line
// (after `↪ 接手自 <old ref>`, spec §8.2 step 7); for write, the reply rule,
// the # HANDOFF headings the mod's completeness check reads and the
// machine facts; for fix, the missing sections and the reply rule.
var RelayPromptFixedParts = RelayPromptSkeleton{
	Write: RelayPromptFixed{
		Head: "[pdx-relay op={{op}} n={{nonce}}] ",
		Tail: lines(
			"- 寫完後只回一行「HANDOFF-WRITTEN」，不要繼續原本的工作。",
			"",
			"格式（每一段都要有，沒有內容就寫「無」）：",
			"# HANDOFF",
			"## 1. 目標與完成定義（使用者要的是什麼、怎樣算完成、範圍外）",
			"## 2. 進度（已完成且驗證 / 進行中停在哪 / 下一步第一個動作具體到指令）",
			"## 3. 檔案異動（git status 與 diff --stat 的結果，加上每個檔案的用途）",
			"## 4. 決策紀錄（選了什麼、為什麼、否決了什麼）",
			"## 5. 死路（試過失敗、不要再試的）",
			"## 6. 環境與指令（測試 / 執行方式）",
			"## 7. 未決問題與需要使用者決定的事",
			"## 8. 協作關係（下面的 pdx 身分；我的 lead 與我管理的 members，沒有就寫無）",
			"",
			"機器提供的事實（請照抄進對應段落）：",
			"- 舊 session id：{{old_session}}",
			"- 舊 ref：{{old_ref}}",
			"- 接力時 context：{{context}}",
			"- pdx 身分：{{whoami}}",
		),
	},
	Fix: RelayPromptFixed{
		Head: "[pdx-relay op={{op}} n={{nonce}}] ",
		Tail: "缺少段落：{{missing}}。請補齊後只回「HANDOFF-WRITTEN」。",
	},
	Seed: RelayPromptFixed{
		Head: "↪ 接手自 {{old_ref}}\n[pdx-relay seed op={{op}} n={{nonce}}] ",
		// The member's open tasks (T-2): the mod fills {{tasks}} from
		// `pdx task mine --seed` (TaskSeedText) and leaves the tail out when
		// there are none. Fixed, not the body's, so an edited seed (U21) still
		// lists them; no body may use it (it is not in RelayPromptVariables).
		Tail: "{{tasks}}",
	},
}

// ErrRelayPromptNotUTF8 is a body that is not UTF-8. Host config also
// returns it for the raw JSON bytes of a field, which it checks before
// decoding (decoding turns an invalid byte into U+FFFD).
var ErrRelayPromptNotUTF8 = errors.New("a relay prompt must be UTF-8")

// relayTag is the mod's machine tag, which no body may hold (U21 (d)).
const relayTag = "[pdx-relay"

// ValidateRelayPromptBody refuses a body over RelayPromptMaxBytes, not
// UTF-8, holding a control character other than \n and \t (so \r, NUL,
// DEL and C1 too), or containing "[pdx-relay" anywhere (U21 (d)). The error
// text is the 400 detail.
func ValidateRelayPromptBody(s string) error {
	if len(s) > RelayPromptMaxBytes {
		return fmt.Errorf("a relay prompt may be at most %d bytes", RelayPromptMaxBytes)
	}
	if !utf8.ValidString(s) {
		return ErrRelayPromptNotUTF8
	}
	for _, r := range s {
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			return fmt.Errorf("a relay prompt may hold no control character but newline and tab (found %U)", r)
		}
	}
	if strings.Contains(s, relayTag) {
		return errors.New("a relay prompt may not contain " + relayTag + ": the machine tag is the mod's")
	}
	return nil
}

// NewRelayPrompts is the GET's answer for the stored bodies: each one
// effective as stored, or the default when it is "" (host config stores
// an empty or whitespace-only body as "").
func NewRelayPrompts(stored RelayPromptBodies) RelayPrompts {
	pick := func(s, def string) string {
		if s == "" {
			return def
		}
		return s
	}
	d := DefaultRelayPromptBodies
	return RelayPrompts{
		Write:     pick(stored.Write, d.Write),
		Fix:       pick(stored.Fix, d.Fix),
		Seed:      pick(stored.Seed, d.Seed),
		Defaults:  d,
		Fixed:     RelayPromptFixedParts,
		Variables: append([]string(nil), RelayPromptVariables...),
	}
}
