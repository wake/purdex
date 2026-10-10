package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	ipeers "github.com/wake/purdex/internal/peers"
)

// A message sent through POST /api/peers/send that ends delivery_uncertain (the receiver's socket write timed out after the frame
// was written, or the sender's own call to the receiver got no answer after the request went out — #1009) may or may not have
// arrived. The commands that send one (`pdx msg send`, `pdx spawn`'s brief, `pdx report`, `pdx task`) say so on stderr and never
// send it again (daemonclient.Once exists for that); the exit code stays what it was — the result is not an error, and a script
// or an agent that re-ran a command on a non-zero exit would send the message twice (#2595).

// peerSendAnswer decodes a /api/peers/send answer leniently: an answer that is not a SendResponse is an empty one (no note),
// exactly as when these callers dropped the body.
type peerSendAnswer struct{ ipeers.SendResponse }

func (a *peerSendAnswer) UnmarshalJSON(b []byte) error {
	_ = json.Unmarshal(b, &a.SendResponse)
	return nil
}

// postPeerMessage is one POST /api/peers/send through client.Once (never replayed) and the answer it got.
func postPeerMessage(ctx context.Context, client *daemonclient.Client, req ipeers.SendRequest) (ipeers.SendResponse, error) {
	var a peerSendAnswer
	_, err := client.Once(ctx, http.MethodPost, "/api/peers/send", req, &a)
	return a.SendResponse, err
}

// noteUncertainSend writes the stderr note when sr is delivery_uncertain: who (the command's name), that the message may not have
// arrived, not to send it again, and how to look. zh picks the language of the command (the team commands speak Chinese, `pdx msg`
// English).
func noteUncertainSend(stderr io.Writer, who string, sr ipeers.SendResponse, zh bool) {
	if sr.Result != ipeers.ResultDeliveryUncertain {
		return
	}
	id := sanitizeCell(sr.MsgID)
	if zh {
		fmt.Fprintf(stderr, "%s: 訊息 %s 送出後沒有收到對方的確認（delivery_uncertain），可能已送達、也可能沒有；不要重送（會重複）。"+
			"用 pdx msg log --tail 10 查它的審計結果，或請對方確認。\n", who, id)
		return
	}
	fmt.Fprintf(stderr, "%s: message %s went out but the receiver did not confirm it (delivery_uncertain); it may or may not have arrived. "+
		"Do not send it again (it would arrive twice). Check its audit row with: pdx msg log --tail 10, or ask the receiver.\n", who, id)
}
