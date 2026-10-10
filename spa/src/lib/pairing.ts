// spa/src/lib/pairing.ts — the Mac side of phone pairing by QR code (QR pairing spec §2 steps 2–4, §3.4, §4), UI-free
// and framework-free: mint a device token on every host, package one pair row per minted host on the relay, poll the
// entry, and settle (revoke or not) when the dialog closes or the countdown ends.
//
// NOTHING HERE THROWS OR REJECTS. Every HTTP exchange is raced against a 10 s timer (an AbortController + setTimeout —
// the host-transfer-api.ts pattern: fake timers cannot advance `AbortSignal.timeout`, and a transport that ignores its
// signal must still end on time).
//
// SECRETS: device tokens live only in the request body of the entry create. They are never logged, never put in an
// error or a returned value; failures are reduced to a reason code.
//
// Revocation rule (spec §4.3): the minted tokens are revoked only after `DELETE entry` answered 204 (an unclaimed entry
// was removed, no phone can claim it any more). 409 → the phone got there first: revoke nothing. 404, any other
// answer, a network error or a timeout → revoke nothing and say the outcome is unknown (the tokens die unused at
// `use_by`; a phone that did pair shows up in the paired-phones list).

//
// Split by concern (#2238): pairing-types (constants, shapes), pairing-transport (HTTP + timeout; internal), pairing-mint (mint +
// package), pairing-session (the state machine). This file is the public surface; importers are unchanged.
export * from './pairing-types'
export { classifyHostsForPairing, mintAndPackage } from './pairing-mint'
export { createPairingSession, type PairingPhase, type PairingSession, type PairingState } from './pairing-session'
