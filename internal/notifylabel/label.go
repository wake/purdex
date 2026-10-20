// Package notifylabel is the label of an agent notification's title: who the session is (a persona's name), which task group
// it belongs to, and its tmux name (notify-title spec §3.1, §3.4). It is a leaf package so the agent module, which fills it
// into the event, and the push module, which turns it into a title, share one type without importing each other.
package notifylabel

// Label is what a notification title is built from. An empty string is "none": a session with no Name keeps today's title
// ({host}：{tmux}); with a Name and no Group the title names the host and tmux session instead of a group.
type Label struct {
	Name  string `json:"name,omitempty"`  // the persona's display name
	Group string `json:"group,omitempty"` // the task group (a team's display name)
	Tmux  string `json:"tmux,omitempty"`  // the tmux session name
}

// IsZero says there is nothing to carry.
func (l Label) IsZero() bool { return l == Label{} }
