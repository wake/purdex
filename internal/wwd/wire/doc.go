// Package wire defines the wwd wire contract: every payload exchanged between
// wwd daemons, the Purdex daemon, the CLI and the App (spec §14a). It holds
// pure types and validation only; no network or file I/O. A change to the
// contract is a PR to this package first.
package wire

// Contract constants.
const (
	// Proto is the highest wire protocol version this build reads, and what /v1/info announces (spec §13.1). 2 adds
	// readers for Facts mem.decompress_rate and queue, Exception allow and flags, the Alert payloads and GET /v1/alerts. 3 adds
	// the reader for Summary.passed_digest (WWD-5b-2r); nothing else changed, in particular no facts record is v 3 (FactsProto).
	Proto = 3
	// FactsProto is the highest `v` a facts record carries and a facts reader takes: 2. It was the same number as Proto while
	// the only proto 2 things were in facts; the proto 3 reader is in a job result, not in a facts record, so a proto 3 build still
	// writes (at most) v 2 facts and a proto 2 host takes them.
	FactsProto = 2
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
