package observation

import (
	"fmt"
	"log"
	"time"
)

// BuilderOption is a functional option for configuring a Builder.
type BuilderOption func(*Builder)

// WithDevMode toggles the over-cap behaviour for DecisionPorts.
// When dev is true, Build() panics if more than 16 DecisionPorts are present.
// When dev is false (default / prod), Build() truncates to 16 and logs a
// warning with prefix [agent][observation].
func WithDevMode(dev bool) BuilderOption {
	return func(b *Builder) {
		b.dev = dev
	}
}

// Builder accumulates fields and produces a validated Observation via Build().
// Safe to reuse across multiple observations within a single goroutine. Not
// thread-safe — callers must hold their own synchronization if needed.
type Builder struct {
	dev bool
	obs Observation
}

// NewBuilder returns a fresh Builder with options applied in order.
func NewBuilder(opts ...BuilderOption) *Builder {
	b := &Builder{}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// TraceID sets the trace_id field. No validation here; validation runs at Build().
func (b *Builder) TraceID(v string) *Builder {
	b.obs.TraceID = v
	return b
}

// SpanID sets the span_id field.
func (b *Builder) SpanID(v string) *Builder {
	b.obs.SpanID = v
	return b
}

// ParentSpanID sets the parent_span_id field.
func (b *Builder) ParentSpanID(v string) *Builder {
	b.obs.ParentSpanID = v
	return b
}

// SessionID sets the session_id field.
func (b *Builder) SessionID(v string) *Builder {
	b.obs.SessionID = v
	return b
}

// ObservedGeneration sets the observed_generation field.
func (b *Builder) ObservedGeneration(v int64) *Builder {
	b.obs.ObservedGeneration = v
	return b
}

// SourceKind sets the source_kind field.
func (b *Builder) SourceKind(v SourceKind) *Builder {
	b.obs.SourceKind = v
	return b
}

// WatcherToken sets the watcher_token field.
func (b *Builder) WatcherToken(v string) *Builder {
	b.obs.WatcherToken = v
	return b
}

// Action sets the action field.
func (b *Builder) Action(v string) *Builder {
	b.obs.Action = v
	return b
}

// Phase sets the phase field.
func (b *Builder) Phase(v ObsPhase) *Builder {
	b.obs.Phase = v
	return b
}

// Proposal sets the proposal field.
func (b *Builder) Proposal(v StateProposal) *Builder {
	b.obs.Proposal = v
	return b
}

// ReasonCode sets the reason_code field.
func (b *Builder) ReasonCode(v string) *Builder {
	b.obs.ReasonCode = v
	return b
}

// ReasonText sets the reason_text field.
func (b *Builder) ReasonText(v string) *Builder {
	b.obs.ReasonText = v
	return b
}

// AddDecisionPort appends a DecisionPort. The cap of 16 is enforced at Build().
func (b *Builder) AddDecisionPort(v DecisionPort) *Builder {
	b.obs.DecisionPorts = append(b.obs.DecisionPorts, v)
	return b
}

// Evidence sets the evidence field.
func (b *Builder) Evidence(v []EvidenceRef) *Builder {
	b.obs.Evidence = v
	return b
}

// ObservedAt sets the observed_at field.
func (b *Builder) ObservedAt(v time.Time) *Builder {
	b.obs.ObservedAt = v
	return b
}

// Seq sets the seq field.
func (b *Builder) Seq(v int64) *Builder {
	b.obs.Seq = v
	return b
}

// Build runs all validation rules defined in D3 and returns the Observation or
// an error wrapping a sentinel. Callers may use errors.Is to match sentinels
// and inspect err.Error() for the specific field name.
//
// DecisionPorts cap (16):
//   - dev mode: panics
//   - prod mode: truncates to 16 and logs "[agent][observation] decision_ports truncated from N to 16"
func (b *Builder) Build() (Observation, error) {
	// --- required string fields ---
	if b.obs.TraceID == "" {
		return Observation{}, fmt.Errorf("%w: %s", ErrMissingRequiredField, "trace_id")
	}
	if b.obs.SessionID == "" {
		return Observation{}, fmt.Errorf("%w: %s", ErrMissingRequiredField, "session_id")
	}

	// --- SourceKind: required + valid ---
	if b.obs.SourceKind == "" {
		return Observation{}, fmt.Errorf("%w: %s", ErrMissingRequiredField, "source_kind")
	}
	if !b.obs.SourceKind.IsValid() {
		return Observation{}, fmt.Errorf("%w: %q", ErrInvalidSourceKind, string(b.obs.SourceKind))
	}

	if b.obs.Action == "" {
		return Observation{}, fmt.Errorf("%w: %s", ErrMissingRequiredField, "action")
	}

	// --- Phase: required + valid ---
	if b.obs.Phase == "" {
		return Observation{}, fmt.Errorf("%w: %s", ErrMissingRequiredField, "phase")
	}
	if !b.obs.Phase.IsValid() {
		return Observation{}, fmt.Errorf("%w: %q", ErrInvalidPhase, string(b.obs.Phase))
	}

	if b.obs.ObservedAt.IsZero() {
		return Observation{}, fmt.Errorf("%w: %s", ErrMissingRequiredField, "observed_at")
	}

	// --- probe requires watcher_token ---
	if b.obs.SourceKind == SourceProbe && b.obs.WatcherToken == "" {
		return Observation{}, ErrMissingWatcherToken
	}

	// --- ActorKey consistency (only when Proposal.ActorKey is non-zero) ---
	ak := b.obs.Proposal.ActorKey
	if ak != (ActorKey{}) {
		if ak.SessionID != b.obs.SessionID {
			return Observation{}, fmt.Errorf("%w: proposal=%q observation=%q",
				ErrActorKeySessionMismatch, ak.SessionID, b.obs.SessionID)
		}
		if ak.Generation != b.obs.ObservedGeneration {
			return Observation{}, fmt.Errorf("%w: proposal=%d observation=%d",
				ErrActorKeyGenerationMismatch, ak.Generation, b.obs.ObservedGeneration)
		}
	}

	// --- DecisionPorts cap ---
	const maxDecisionPorts = 16
	if n := len(b.obs.DecisionPorts); n > maxDecisionPorts {
		if b.dev {
			panic(fmt.Sprintf("[agent][observation] decision_ports cap exceeded: %d > %d", n, maxDecisionPorts))
		}
		log.Printf("[agent][observation] decision_ports truncated from %d to %d", n, maxDecisionPorts)
		b.obs.DecisionPorts = b.obs.DecisionPorts[:maxDecisionPorts]
	}

	out := b.obs
	return out, nil
}
