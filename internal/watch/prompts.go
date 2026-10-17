package watch

// The built-in prompts the watcher is started and woken with (host-load-control spec §12.4); `start_prompt` and `wake_prompt` in the
// watch setting replace them when set. They are short on purpose: what the watcher is, how it works and what it may and may not do
// is in its skill (skills/pdx-watch/SKILL.md), which these only point at.
const (
	// DefaultStartPrompt starts a new watcher.
	DefaultStartPrompt = "你是這台主機的負載守門員。請先讀 pdx-watch skill，再依它處理待辦的負載事件。"
	// DefaultWakePrompt resumes a resting one; the request's prompt (what woke it) follows after a space.
	DefaultWakePrompt = "你被叫醒了。請讀 pdx-watch skill 的「醒來」一節，再處理待辦事件與下面這則說明。"
)
