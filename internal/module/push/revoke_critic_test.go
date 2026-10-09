package push

import (
	"context"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/push/apns"
)

// onSendAPNS runs a hook after each Send (to revoke "while the request is in flight").
type onSendAPNS struct {
	*fakeAPNS
	hook func()
}

func (o onSendAPNS) Send(ctx context.Context, env, token string, h apns.Headers, payload []byte) apns.Result {
	res := o.fakeAPNS.Send(ctx, env, token, h, payload)
	o.hook()
	return res
}

// A registration dropped between the first send and its retry is not sent the retry (either retry kind).
func TestSender_NoRetryToARegistrationDroppedMeanwhile(t *testing.T) {
	for name, first := range map[string]apns.Result{
		"retry later":  {Class: apns.RetryLater, Status: 503, Reason: "ServiceUnavailable"},
		"jwt rejected": {Class: apns.JWTRejected, Status: 403, Reason: "ExpiredProviderToken"},
	} {
		d := device(strings.Repeat("a1", 32), "en", "mlab")
		book := newBook(d)
		inner := &fakeAPNS{script: []apns.Result{first, {Class: apns.OK, Status: 200}}}
		s := newTestSender(book, inner, &slept{})
		s.apns = onSendAPNS{inner, func() { book.Remove(d.DeviceID) }}
		runOne(t, s, leadJob(d.DeviceID))
		if inner.count() != 1 {
			t.Fatalf("%s: %d sends, want 1 (no retry after the registration was dropped)", name, inner.count())
		}
	}
}

// The send cache is cleaned even when the database cannot be written: nothing owned by a revoked phone stays sendable.
func TestDropOwned_FailsClosedWhenTheStoreCannotBeWritten(t *testing.T) {
	e := newRevokeEnv(t)
	pa := e.pair(t, pairA)
	e.register(t, &pa, tokA)
	e.register(t, nil, tokB)
	must(t, e.push.store.Close()) // every write now fails
	e.push.dropOwned([]string{pa.ID})
	if _, ok := e.push.Get(push.DeviceID(tokA)); ok {
		t.Fatal("the revoked phone's registration is still sendable")
	}
	if _, ok := e.push.Get(push.DeviceID(tokB)); !ok {
		t.Fatal("the admin's registration was dropped")
	}
}
