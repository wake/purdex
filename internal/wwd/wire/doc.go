// Package wire defines the wwd wire contract: every payload exchanged between
// wwd daemons, the Purdex daemon, the CLI and the App (spec §14a). It holds
// pure types and validation only; no network or file I/O. A change to the
// contract is a PR to this package first.
package wire

// Contract constants.
const (
	// Proto is the highest wire protocol version this build reads, and what /v1/info announces (spec §13.1). 2 adds
	// readers for Facts mem.decompress_rate and queue, Exception allow and flags, the Alert payloads and GET /v1/alerts.
	Proto = 2
	// MinProto is the oldest version a reader still takes: a v1 payload never has the fields proto 2 added.
	MinProto = 1
	// WriteProto is the `v` writers put in facts. It stays 1, and the new fields stay empty, until every pool host reports
	// proto >= 2 in /v1/info (spec §13.1, the two phases); the PR that fills them raises it.
	WriteProto = 1
	// LeaseS and RenewS are the default relative lease and renewal seconds.
	LeaseS = 60
	RenewS = 15
	// Port is the TCP port wwd listens on (spec §2).
	Port = 7871
)
