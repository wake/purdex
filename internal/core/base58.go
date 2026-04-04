package core

import (
	"encoding/binary"
	"errors"
	"math/big"
	"net"
	"strings"
)

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

var (
	big58            = big.NewInt(58)
	big0             = big.NewInt(0)
	errBase58Invalid = errors.New("invalid base58 character")
	errPairingDecode = errors.New("invalid pairing code")
)

// base58Encode encodes bytes to a base58 string.
func base58Encode(data []byte) string {
	n := new(big.Int).SetBytes(data)
	var result []byte
	mod := new(big.Int)
	for n.Cmp(big0) > 0 {
		n.DivMod(n, big58, mod)
		result = append(result, base58Alphabet[mod.Int64()])
	}
	// Leading zero bytes → leading '1's
	for _, b := range data {
		if b != 0 {
			break
		}
		result = append(result, '1')
	}
	// Reverse
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return string(result)
}

// base58EncodeFixed encodes and left-pads with '1' to fixed length.
func base58EncodeFixed(data []byte, length int) string {
	s := base58Encode(data)
	for len(s) < length {
		s = "1" + s
	}
	return s
}

// base58Decode decodes a base58 string back to bytes.
func base58Decode(s string) ([]byte, error) {
	n := new(big.Int)
	for _, c := range s {
		idx := strings.IndexRune(base58Alphabet, c)
		if idx < 0 {
			return nil, errBase58Invalid
		}
		n.Mul(n, big58)
		n.Add(n, big.NewInt(int64(idx)))
	}
	result := n.Bytes()
	// Restore leading zero bytes from leading '1's
	for _, c := range s {
		if c != '1' {
			break
		}
		result = append([]byte{0}, result...)
	}
	return result, nil
}

// EncodePairingCode encodes IP + port + secret into a 13-char formatted code.
func EncodePairingCode(ip net.IP, port uint16, secret []byte) string {
	ip4 := ip.To4()
	if ip4 == nil {
		ip4 = net.IP{0, 0, 0, 0}
	}
	buf := make([]byte, 9)
	copy(buf[0:4], ip4)
	binary.BigEndian.PutUint16(buf[4:6], port)
	copy(buf[6:9], secret)
	raw := base58EncodeFixed(buf, 13)
	return FormatPairingCode(raw)
}

// FormatPairingCode inserts dashes: XXXX-XXXX-XXXXX (4-4-5).
func FormatPairingCode(raw string) string {
	if len(raw) < 13 {
		return raw
	}
	return raw[:4] + "-" + raw[4:8] + "-" + raw[8:13]
}

// DecodePairingCode decodes a pairing code string into IP, port, and secret.
// Strips dashes, slashes, and whitespace before decoding.
func DecodePairingCode(code string) (ip net.IP, port uint16, secret []byte, err error) {
	// Clean input
	cleaned := strings.Map(func(r rune) rune {
		if r == '-' || r == '/' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, code)

	data, err := base58Decode(cleaned)
	if err != nil {
		return nil, 0, nil, errPairingDecode
	}

	// Pad to 9 bytes if shorter (leading zeros lost in encoding)
	for len(data) < 9 {
		data = append([]byte{0}, data...)
	}
	if len(data) != 9 {
		return nil, 0, nil, errPairingDecode
	}

	ip = net.IP(data[0:4])
	port = binary.BigEndian.Uint16(data[4:6])
	secret = data[6:9]
	return ip, port, secret, nil
}
