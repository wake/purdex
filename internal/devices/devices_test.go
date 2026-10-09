package devices

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

// QP-1 task 1/2: the device token format, its hash, and the principal a request carries.

var wantTokenRe = regexp.MustCompile(`^pdxd_[0-9a-f]{32}$`)

func TestNewToken_FormatAndUniqueness(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		tok, err := NewToken()
		if err != nil {
			t.Fatal(err)
		}
		if !wantTokenRe.MatchString(tok) {
			t.Fatalf("token has the wrong shape (len %d)", len(tok))
		}
		if seen[tok] {
			t.Fatal("two tokens were equal")
		}
		seen[tok] = true
	}
}

func TestHash_IsSHA256LowercaseHexAndNotTheToken(t *testing.T) {
	tok, _ := NewToken()
	h := Hash(tok)
	if len(h) != 64 || h != strings.ToLower(h) || strings.Contains(h, tok[5:]) {
		t.Fatalf("hash = %d chars", len(h))
	}
	if Hash(tok) != h {
		t.Fatal("not deterministic")
	}
	// A known vector, so the hash is the plain SHA-256 of the token's bytes, not something salted or truncated.
	if got := Hash("abc"); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("Hash(abc) = %s", got)
	}
}

func TestIsDeviceToken(t *testing.T) {
	tok, _ := NewToken()
	if !IsDeviceToken(tok) {
		t.Fatal("a device token was not recognised")
	}
	for _, s := range []string{"", "pdxd_", "pdxp_" + tok[5:], "Bearer " + tok, "pdxd_" + strings.Repeat("g", 32), tok + "x", tok[:len(tok)-1]} {
		if IsDeviceToken(s) {
			t.Fatalf("%q was taken for a device token", s)
		}
	}
}

func TestDeviceID_FormatAndClientID(t *testing.T) {
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^d_[0-9a-f]{12}$`).MatchString(id) {
		t.Fatalf("id = %q", id)
	}
	if !ValidID(id) || ValidID("d_xyz") || ValidID("") || ValidID(id+"0") {
		t.Fatal("ValidID wrong")
	}
	// The profile client id a device writes under is c_ + the id's 12 hex (spec 5.2).
	if got := ClientID(id); got != "c_"+id[2:] {
		t.Fatalf("ClientID = %q", got)
	}
}

func TestPrincipalInContext(t *testing.T) {
	if _, ok := PrincipalFrom(context.Background()); ok {
		t.Fatal("a plain context has a principal")
	}
	p := Principal{ID: "d_aaaaaaaaaaaa", PairingID: "pair", ProfileID: "p_1"}
	got, ok := PrincipalFrom(WithPrincipal(context.Background(), p))
	if !ok || got != p {
		t.Fatalf("got %+v ok %v", got, ok)
	}
}
