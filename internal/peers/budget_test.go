package peers

import (
	"testing"
	"time"
)

// #1009: every layer of a remote send waits longer than the worst case of what it waits for, so a delivery that lands is never
// reported as failed because a layer above gave up first. Mutation gate: make any layer equal to (or shorter than) its
// downstream -> red.
func TestBudgetChain_EachLayerOutlastsItsDownstream(t *testing.T) {
	if ReceiverWorstCase != InventoryBudget+HelperReadyTimeout+SocketWriteTimeout {
		t.Errorf("ReceiverWorstCase = %v is not inventory + helper + socket write", ReceiverWorstCase)
	}
	if InterDaemonTimeout < ReceiverWorstCase+TimeoutMargin {
		t.Errorf("InterDaemonTimeout %v must outlast the receiver's worst case %v by the margin %v", InterDaemonTimeout, ReceiverWorstCase, TimeoutMargin)
	}
	if SenderWorstCase != InventoryBudget+RemoteFetchTimeout+InterDaemonTimeout {
		t.Errorf("SenderWorstCase = %v is not inventory + fetch + the inter-daemon call", SenderWorstCase)
	}
	if SendRequestTimeout < SenderWorstCase+TimeoutMargin {
		t.Errorf("the CLI's SendRequestTimeout %v must outlast the sender daemon's worst case %v by the margin %v", SendRequestTimeout, SenderWorstCase, TimeoutMargin)
	}
	if TimeoutMargin <= 0 || TimeoutMargin < time.Second {
		t.Errorf("TimeoutMargin %v is no margin", TimeoutMargin)
	}
}
