package codex

// HasReadiness reports whether this agent can derive detailed readiness
// (waiting / error / clear) from tmux pane content. Stub checker returns
// StatusRunning, so this stays false until a real implementation lands.
// Lights spec: §3.6.1, §6.4.
const HasReadiness = false
