package execution

import (
	"errors"
	"fmt"
	"strings"
)

// Profile is the M0 sandbox profile (spec §8.1 / m0-contract §6). The four values
// form a TOTAL order from strictest to widest:
//
//	read-only ⊏ ask ⊏ workspace-write ⊏ danger-full
//
// The iota ordering encodes that order directly, so `min` (the clamp) is just the
// numerically smaller value. Sandbox here is blast-radius reduction, not hard
// isolation — OS-level isolation (container/VM) is deferred, but the enum and its
// clamp are frozen in M0 so both sides can mock against them.
type Profile int

const (
	ProfileReadOnly       Profile = iota // strictest — plan-only, no writes
	ProfileAsk                           // prompt on each action
	ProfileWorkspaceWrite                // auto-accept workspace edits
	ProfileDangerFull                    // widest — bypass all permissions
)

// DefaultHostPolicy is the least-privilege fallback used when the daemon host
// policy is unset (spec §8.1: host unset → strictest usable default, NOT
// danger-full).
const DefaultHostPolicy = ProfileAsk

// ErrUnknownProfile is returned when a profile string is not one of the four M0
// enum values. It maps to the `unknown_sandbox_profile` error code and a 422 on
// the wire (m0-contract §6/§7); a request carrying it fails closed — never
// silently widened nor silently narrowed.
var ErrUnknownProfile = errors.New("unknown sandbox_profile")

// ParseProfile parses a wire profile string onto a Profile. An empty string is
// NOT valid here (the "omitted request" default is resolved by Clamp, not
// ParseProfile); anything outside the four enum values → ErrUnknownProfile.
func ParseProfile(s string) (Profile, error) {
	switch s {
	case "read-only":
		return ProfileReadOnly, nil
	case "ask":
		return ProfileAsk, nil
	case "workspace-write":
		return ProfileWorkspaceWrite, nil
	case "danger-full":
		return ProfileDangerFull, nil
	default:
		return 0, fmt.Errorf("%w: %s", ErrUnknownProfile, s)
	}
}

// String renders the canonical wire spelling of the profile (what is persisted on
// the execution row and echoed as effective_sandbox_profile).
func (p Profile) String() string {
	switch p {
	case ProfileReadOnly:
		return "read-only"
	case ProfileAsk:
		return "ask"
	case ProfileWorkspaceWrite:
		return "workspace-write"
	case ProfileDangerFull:
		return "danger-full"
	default:
		return fmt.Sprintf("Profile(%d)", int(p))
	}
}

// ToPermissionMode maps the profile onto the `claude --permission-mode` value
// (spec §8.1 mapping table, M0 claude only). codex's mapping lands in M1.
func (p Profile) ToPermissionMode() string {
	switch p {
	case ProfileReadOnly:
		return "plan"
	case ProfileAsk:
		return "default"
	case ProfileWorkspaceWrite:
		return "acceptEdits"
	case ProfileDangerFull:
		return "bypassPermissions"
	default:
		return ""
	}
}

// ResolveHostPolicy resolves the daemon host sandbox policy from its configured
// string. Empty/unset → DefaultHostPolicy (ask, least privilege). An unknown
// value is a misconfiguration and fails closed with ErrUnknownProfile; the caller
// decides whether to reject startup or fall back to the strict default.
func ResolveHostPolicy(s string) (Profile, error) {
	if strings.TrimSpace(s) == "" {
		return DefaultHostPolicy, nil
	}
	return ParseProfile(s)
}

// Clamp computes the effective profile for one dispatch: effective =
// min(request, hostPolicy) by the total order (only ever narrows, never widens).
//
//   - request omitted (empty) → the host policy itself (spec §8.1 "omitted →
//     host default"; hostPolicy already carries the unset→ask resolution).
//   - request an unknown enum → ErrUnknownProfile (fail closed).
//   - otherwise → the stricter of request and hostPolicy.
//
// hostPolicy MUST be an already-resolved Profile (see ResolveHostPolicy), so the
// daemon host policy is the sole authority and a dispatch can only ever request.
func Clamp(request string, hostPolicy Profile) (Profile, error) {
	if strings.TrimSpace(request) == "" {
		return hostPolicy, nil
	}
	req, err := ParseProfile(request)
	if err != nil {
		return 0, err
	}
	if req < hostPolicy {
		return req, nil
	}
	return hostPolicy, nil
}
