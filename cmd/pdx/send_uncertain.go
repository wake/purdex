package main

import (
	"bytes"
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

// resultUnreadable stands for an answer that is not JSON at all (a proxy's page, a body cut short): nothing says it was delivered.
const resultUnreadable = "unreadable" // an empty or non-JSON 200 too

// readPeerSendAnswer reads a /api/peers/send 200 body. No body, or not JSON at all, is resultUnreadable; JSON of another shape (an old daemon's,
// a test's) is an empty answer with no note — as when these callers dropped the body.
func readPeerSendAnswer(raw []byte) ipeers.SendResponse {
	var sr ipeers.SendResponse
	// No body, or one that is not JSON: the daemon never answers a send like that, so something cut or rewrote the answer.
	if len(bytes.TrimSpace(raw)) == 0 || !json.Valid(raw) {
		return ipeers.SendResponse{Result: resultUnreadable}
	}
	_ = json.Unmarshal(raw, &sr)
	return sr
}

// postPeerMessage is one POST /api/peers/send through client.Once (never replayed) and the answer it got.
func postPeerMessage(ctx context.Context, client *daemonclient.Client, req ipeers.SendRequest) (ipeers.SendResponse, error) {
	var raw daemonclient.RawBody
	_, err := client.Once(ctx, http.MethodPost, "/api/peers/send", req, &raw)
	if err != nil {
		return ipeers.SendResponse{}, err
	}
	return readPeerSendAnswer(raw), nil
}

// noteUncertainSend writes the stderr note when sr is delivery_uncertain: who (the command's name), that the message may not have
// arrived, not to send it again, and how to look. zh picks the language of the command (the team commands speak Chinese, `pdx msg`
// English).
func noteUncertainSend(stderr io.Writer, who string, sr ipeers.SendResponse, zh bool) {
	if sr.Result == resultUnreadable {
		if zh {
			fmt.Fprintf(stderr, "%s: 訊息送出了，但 daemon 的回應讀不懂（可能被截斷或被代理改寫），無法確認有沒有送達；不要重送（會重複）。"+
				"用 pdx msg log --tail 10 查審計結果，或請對方確認。\n", who)
		} else {
			fmt.Fprintf(stderr, "%s: the message went out but the daemon's answer could not be read (cut short or rewritten on the way), so nobody knows whether it arrived. "+
				"Do not send it again (it would arrive twice). Check its audit row with: pdx msg log --tail 10, or ask the receiver.\n", who)
		}
		return
	}
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
