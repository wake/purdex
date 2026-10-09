package peers

import (
	"context"
	"errors"
	"net/http"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
)

// The in-process sender runs handleSend as the admin principal (adopt plan PL-1d1, plan v3 P6-1).

func TestSender_RunsHandleSendAsAdmin(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	p := s.addLocalPeer(localPeerName, localPeerSessionID, localPeerPID)
	resp, err := moduleSender{m: s.m}.Send(context.Background(), s.localSendReq())
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	p.recvLine()
	if resp.Result != ipeers.ResultDelivered || !ipeers.IsUUID(resp.MsgID) {
		t.Fatalf("response = %+v", resp)
	}
	if rows := s.rows(); len(rows) != 1 {
		t.Errorf("audit rows = %d, want the one send path to leave one", len(rows))
	}
}

func TestSender_RefusalIsASendError(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	s.addLocalPeer(localPeerName, localPeerSessionID, localPeerPID)
	req := s.localSendReq()
	req.To = localAlias + "/no-such-conversation"
	_, err := moduleSender{m: s.m}.Send(context.Background(), req)
	var se *SendError
	if !errors.As(err, &se) || se.Status != http.StatusNotFound || se.API.Error != ipeers.ErrPeerNotFound {
		t.Fatalf("err = %v, want a SendError 404 %s", err, ipeers.ErrPeerNotFound)
	}
}
