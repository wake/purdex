package core

import (
	"net"
	"testing"
)

func TestBase58RoundTrip(t *testing.T) {
	original := []byte{100, 64, 0, 2, 0x1e, 0xb4, 0xab, 0xcd, 0xef} // 9 bytes
	encoded := base58Encode(original)
	decoded, err := base58Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != len(original) {
		t.Fatalf("length mismatch: want %d, got %d", len(original), len(decoded))
	}
	for i := range original {
		if decoded[i] != original[i] {
			t.Errorf("byte %d: want %d, got %d", i, original[i], decoded[i])
		}
	}
}

func TestBase58FixedLength(t *testing.T) {
	// Smallest 9-byte value: all zeros
	small := make([]byte, 9)
	enc := base58EncodeFixed(small, 13)
	if len(enc) != 13 {
		t.Errorf("want 13 chars, got %d: %s", len(enc), enc)
	}

	// Largest 9-byte value
	big := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	enc2 := base58EncodeFixed(big, 13)
	if len(enc2) != 13 {
		t.Errorf("want 13 chars, got %d: %s", len(enc2), enc2)
	}
}

func TestEncodePairingCode(t *testing.T) {
	ip := net.ParseIP("100.64.0.2").To4()
	port := uint16(7860)
	secret := []byte{0xab, 0xcd, 0xef}

	code := EncodePairingCode(ip, port, secret)

	// Should be formatted as XXXX-XXXX-XXXXX
	if len(code) != 15 { // 13 chars + 2 dashes
		t.Errorf("want 15 chars (with dashes), got %d: %s", len(code), code)
	}
	if code[4] != '-' || code[9] != '-' {
		t.Errorf("wrong dash positions: %s", code)
	}
}

func TestDecodePairingCode(t *testing.T) {
	ip := net.ParseIP("100.64.0.2").To4()
	port := uint16(7860)
	secret := []byte{0xab, 0xcd, 0xef}

	code := EncodePairingCode(ip, port, secret)
	gotIP, gotPort, gotSecret, err := DecodePairingCode(code)
	if err != nil {
		t.Fatal(err)
	}
	if !gotIP.Equal(ip) {
		t.Errorf("ip: want %s, got %s", ip, gotIP)
	}
	if gotPort != port {
		t.Errorf("port: want %d, got %d", port, gotPort)
	}
	for i := range secret {
		if gotSecret[i] != secret[i] {
			t.Errorf("secret byte %d: want %d, got %d", i, secret[i], gotSecret[i])
		}
	}
}

func TestDecodePairingCodeWithSpaces(t *testing.T) {
	ip := net.ParseIP("100.64.0.2").To4()
	code := EncodePairingCode(ip, 7860, []byte{0xab, 0xcd, 0xef})
	// Add extra whitespace and slashes
	messy := " " + code[:4] + " / " + code[5:9] + "  " + code[10:] + " "
	gotIP, _, _, err := DecodePairingCode(messy)
	if err != nil {
		t.Fatalf("should decode messy input: %v", err)
	}
	if !gotIP.Equal(ip) {
		t.Errorf("ip: want %s, got %s", ip, gotIP)
	}
}

func TestDecodePairingCodeInvalid(t *testing.T) {
	_, _, _, err := DecodePairingCode("not-valid")
	if err == nil {
		t.Error("expected error for invalid code")
	}
}

func TestFormatPairingCode(t *testing.T) {
	raw := "1234567890abc"
	formatted := FormatPairingCode(raw)
	if formatted != "1234-5678-90abc" {
		t.Errorf("want 1234-5678-90abc, got %s", formatted)
	}
}
