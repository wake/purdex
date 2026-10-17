// Package wire defines the wwd wire contract: every payload exchanged between
// wwd daemons, the Purdex daemon, the CLI and the App (spec §14a). It holds
// pure types and validation only; no network or file I/O. A change to the
// contract is a PR to this package first.
package wire

// Contract constants.
const (
	// Proto is the wire protocol version carried in facts (`v`) and /v1/info.
	Proto = 1
	// LeaseS and RenewS are the default relative lease and renewal seconds.
	LeaseS = 60
	RenewS = 15
	// Port is the TCP port wwd listens on (spec §2).
	Port = 7871
)
