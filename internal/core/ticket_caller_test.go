package core

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/devices"
)

// QP-1 task 3: a ticket records who asked for it and gives it back, once.

func TestTicketStore_AdminAndDeviceTicketsComeBackWithTheirCaller(t *testing.T) {
	ts := NewTicketStore()
	p := devices.Principal{ID: "d_aaaaaaaaaaaa", PairingID: "pair-1", ProfileID: "p_0123456789ab"}

	adminTicket, err := ts.GenerateFor(devices.Caller{Admin: true})
	require.NoError(t, err)
	deviceTicket, err := ts.GenerateFor(devices.Caller{Device: &p})
	require.NoError(t, err)

	c, ok := ts.ValidateCaller(adminTicket)
	require.True(t, ok)
	assert.True(t, c.Admin)
	assert.Nil(t, c.Device)

	c, ok = ts.ValidateCaller(deviceTicket)
	require.True(t, ok)
	assert.False(t, c.Admin)
	require.NotNil(t, c.Device)
	assert.Equal(t, p, *c.Device)
}

// Validate-and-consume is one step: a second validation of the same ticket fails, whichever method asks.
func TestTicketStore_AConsumedTicketIsGoneForBothMethods(t *testing.T) {
	ts := NewTicketStore()
	tk, _ := ts.GenerateFor(devices.Caller{Admin: true})
	_, ok := ts.ValidateCaller(tk)
	require.True(t, ok)
	_, ok = ts.ValidateCaller(tk)
	assert.False(t, ok, "second use")
	assert.False(t, ts.Validate(tk), "bool Validate after consumption")

	tk2, _ := ts.GenerateFor(devices.Caller{Admin: true})
	assert.True(t, ts.Validate(tk2), "the bool method still validates and consumes")
	_, ok = ts.ValidateCaller(tk2)
	assert.False(t, ok)
}

// A ticket made without a caller is anonymous, exactly as tickets were before principals: valid, with no identity.
func TestTicketStore_GenerateIsAnonymous(t *testing.T) {
	ts := NewTicketStore()
	tk, _ := ts.Generate()
	c, ok := ts.ValidateCaller(tk)
	require.True(t, ok)
	assert.False(t, c.Admin)
	assert.Nil(t, c.Device)
}

// The ticket keeps its own copy of the principal: changing the one it was made from changes nothing.
func TestTicketStore_TheTicketHoldsACopyOfThePrincipal(t *testing.T) {
	ts := NewTicketStore()
	p := devices.Principal{ID: "d_aaaaaaaaaaaa"}
	tk, _ := ts.GenerateFor(devices.Caller{Device: &p})
	p.ID = "d_bbbbbbbbbbbb"
	c, _ := ts.ValidateCaller(tk)
	assert.Equal(t, "d_aaaaaaaaaaaa", c.Device.ID)
}

func TestTicketStore_ExpiredAndUnknownTicketsAreRefusedByValidateCaller(t *testing.T) {
	ts := NewTicketStore()
	for _, tk := range []string{"", "nope"} {
		_, ok := ts.ValidateCaller(tk)
		assert.False(t, ok, "%q", tk)
	}
}

// POST /api/ws-ticket mints the ticket for the caller of that request.
func TestHandleWsTicket_MintsForTheCallerOfTheRequest(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{}})
	p := devices.Principal{ID: "d_aaaaaaaaaaaa", PairingID: "pair-1"}
	for name, ctx := range map[string]context.Context{
		"admin":     devices.WithAdmin(context.Background()),
		"device":    devices.WithPrincipal(context.Background(), p),
		"anonymous": context.Background(),
	} {
		rec := httptest.NewRecorder()
		c.handleWsTicket(rec, httptest.NewRequest("POST", "/api/ws-ticket", nil).WithContext(ctx))
		require.Equal(t, 200, rec.Code, name)
		var body struct{ Ticket string }
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
		got, ok := c.Tickets.ValidateCaller(body.Ticket)
		require.True(t, ok, name)
		want := devices.CallerFrom(ctx)
		assert.Equal(t, want.Admin, got.Admin, name)
		assert.Equal(t, want.Device == nil, got.Device == nil, name)
		if want.Device != nil {
			assert.Equal(t, *want.Device, *got.Device, name)
		}
	}
}
