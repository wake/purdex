package advisor

import (
	"errors"
	"fmt"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// SilenceAfter is how long a host or the local Purdex daemon must have been silent before it is told. One number for every host (spec
// §13.3, 10-19: the matrix said 3 minutes, an earlier line of the mserv row said 2; 3 is settled), long enough for a restart of a wwd
// (about 15 s) or a Purdex daemon (about 30 s) and the poll that follows, short enough to be told within the hour of a real failure.
const SilenceAfter = 3 * time.Minute

// PeerDown is what this host's pool poller knows of one peer: Since is when it stopped answering (zero: it answers now).
type PeerDown struct {
	Alias string
	Since time.Time
}

// PeerSilence is "silence" (push) for one peer wwd: the pool reads of that host have failed for SilenceAfter. A host cannot report its
// own absence, so the other hosts' wwds notice it and the deliverer tells it (spec §13.3). It is made only for a peer whose
// alert_unreachable is on (off for air19, which is a laptop that sleeps), and its key names the peer, so each host's absence is one alert
// that clears the moment the peer answers again. mserv's "unreachable" condition is this same alert.
type PeerSilence struct{ Alias string }

func (PeerSilence) Kind() wire.AlertKind { return wire.AlertSilence }

// SilenceKey is the alert key for a silent peer.
func SilenceKey(alias string) string { return "silence:" + alias }

func (r PeerSilence) Eval(in Input) (Verdict, error) {
	v := Verdict{Key: SilenceKey(r.Alias), Level: wire.AlertPush}
	if in.Peers == nil {
		return Verdict{}, errors.New("the pool view is not available")
	}
	for _, p := range in.Peers() {
		if p.Alias != r.Alias {
			continue
		}
		if p.Since.IsZero() || in.Now.Sub(p.Since) < SilenceAfter {
			return v, nil
		}
		v.Holds, v.Detail = true, fmt.Sprintf("%s 的 wwd 已 %d 分鐘連不上", r.Alias, int(in.Now.Sub(p.Since)/time.Minute))
		return v, nil
	}
	return Verdict{}, fmt.Errorf("%s is not a peer of this host", r.Alias)
}

// PartnerDown is "silence" (push) for the Purdex daemon of this host: its deliverer reads this host's alerts every 30 s, and none has
// for SilenceAfter. The wwd notices (the daemon cannot report its own absence), and the other deliverer tells it (air26's, when the daemon
// down is mlab's). It is made only where the host has a Purdex daemon ([advisor] partner_check). A wwd that has just started counts from
// its start, so a daemon that never comes back is told too.
type PartnerDown struct{}

func (PartnerDown) Kind() wire.AlertKind { return wire.AlertSilence }

func (PartnerDown) Eval(in Input) (Verdict, error) {
	v := Verdict{Key: "partner_down", Level: wire.AlertPush}
	if in.PartnerSeen == nil {
		return Verdict{}, errors.New("the partner's last read is not available")
	}
	seen := in.PartnerSeen()
	if gap := in.Now.Sub(seen); gap >= SilenceAfter {
		v.Holds, v.Detail = true, fmt.Sprintf("這台的 Purdex daemon 已 %d 分鐘沒有來讀告警", int(gap/time.Minute))
	}
	return v, nil
}
