package main

import (
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
)

// #1009: the CLI waits for the sender daemon's whole worst case. Mutation gate: msgSendTimeout back to a literal below it -> red.
func TestMsgSendTimeout_OutlastsTheSenderDaemon(t *testing.T) {
	if msgSendTimeout != ipeers.SendRequestTimeout {
		t.Errorf("msgSendTimeout = %v, want ipeers.SendRequestTimeout %v", msgSendTimeout, ipeers.SendRequestTimeout)
	}
	if msgSendTimeout < ipeers.SenderWorstCase+ipeers.TimeoutMargin {
		t.Errorf("msgSendTimeout %v does not outlast the sender daemon's worst case %v", msgSendTimeout, ipeers.SenderWorstCase)
	}
	if briefTimeout < ipeers.SenderWorstCase+ipeers.TimeoutMargin {
		t.Errorf("briefTimeout %v does not outlast the sender daemon's worst case %v", briefTimeout, ipeers.SenderWorstCase)
	}
}
