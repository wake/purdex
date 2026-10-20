package daemon

import (
	"context"
	"errors"
	"net"
	"net/url"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// A peer whose wwd does not answer may be a stopped wwd on a host that is up, or a host that is off or cut off. The difference is looked
// at once the wwd cannot be read, and only then: a TCP connect to the SSH port of the peer's address, closed the moment it succeeds. Nothing
// is sent, no login is tried, nothing is read from the host — only whether something answers there.

const (
	// livenessPort is the port that is looked at.
	livenessPort = "22"
	// livenessTimeout bounds the connect.
	livenessTimeout = 2 * time.Second
)

// answeredOverHTTP says whether a failed read was an answer: an error status, a body that could not be read as what it should be, or no
// error at all (the answer was from another name). Only a failure to talk to the address (refused, reset, timed out, no route) is not.
func answeredOverHTTP(err error) bool {
	var ue *url.Error
	return !errors.As(err, &ue)
}

// look makes the look at peer i and files what it found; the pool lock is not held while the connect goes on. A look cut short because
// this wwd is stopping says nothing and leaves what was there.
func (p *PoolPoller) look(ctx context.Context, i int) {
	host, _, err := net.SplitHostPort(p.peers[i].HostPort)
	if err != nil {
		return
	}
	dctx, cancel := context.WithTimeout(ctx, livenessTimeout)
	defer cancel()
	conn, err := p.dial(dctx, "tcp", net.JoinHostPort(host, livenessPort))
	if ctx.Err() != nil {
		return
	}
	if err == nil {
		_ = conn.Close()
	}
	p.mu.Lock()
	p.setLiveness(i, livenessOf(err))
	p.mu.Unlock()
}

// setLiveness files peer i's liveness; the caller holds mu. LivenessAt is the time of this look; LivenessSince moves only when the value
// does (and is set when the peer comes back to ok as well), so it says how long the peer has been in the state it is in.
func (p *PoolPoller) setLiveness(i int, l wire.Liveness) {
	at := p.now()
	if p.state[i].Liveness != l || p.state[i].LivenessSince == nil {
		since := at
		p.state[i].LivenessSince = &since
	}
	p.state[i].Liveness, p.state[i].LivenessAt = l, &at
}

// livenessOf reads the result of the connect: it went through, or it was refused (something there answered, with a refusal): the host is
// up and the wwd is what stopped. Any other failure (no answer in time, no route, network unreachable): nothing answers.
func livenessOf(err error) wire.Liveness {
	if err == nil || errors.Is(err, syscall.ECONNREFUSED) {
		return wire.LivenessWwdDown
	}
	return wire.LivenessHostDown
}
