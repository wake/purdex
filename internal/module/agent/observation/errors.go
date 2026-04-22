package observation

import "errors"

// ErrMissingRequiredField is returned by Builder.Build when a required field
// is empty or zero. The error is wrapped with the field name so callers can
// inspect it via err.Error() while still using errors.Is for type checking.
var ErrMissingRequiredField = errors.New("observation: required field missing")

// ErrInvalidSourceKind is returned by Builder.Build when SourceKind is set to
// a non-empty value that is not one of the five defined constants.
var ErrInvalidSourceKind = errors.New("observation: invalid source kind")

// ErrInvalidPhase is returned by Builder.Build when Phase is set to a
// non-empty value that is not one of the three defined ObsPhase constants.
var ErrInvalidPhase = errors.New("observation: invalid phase")

// ErrMissingWatcherToken is returned by Builder.Build when SourceKind is
// SourceProbe but WatcherToken is empty.
var ErrMissingWatcherToken = errors.New("observation: probe observation missing watcher_token")

// ErrActorKeySessionMismatch is returned by Builder.Build when
// Proposal.ActorKey.SessionID does not equal the Observation's SessionID.
var ErrActorKeySessionMismatch = errors.New("observation: proposal actor key session mismatch")

// ErrActorKeyGenerationMismatch is returned by Builder.Build when
// Proposal.ActorKey.Generation does not equal the Observation's
// ObservedGeneration.
var ErrActorKeyGenerationMismatch = errors.New("observation: proposal actor key generation mismatch")
