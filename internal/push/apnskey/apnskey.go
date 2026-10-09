// Package apnskey loads the APNs auth key of the push module (push spec docs/specs/2026-10-09-push-spec.md §3).
//
// The directory is opened as an os.Root and config.env and the key file are read through it: a name the root refuses
// (absolute, "..", a symlink that leaves the directory, a swap between check and open) is an error. Only
// APNS_KEY_ID, APNS_TEAM_ID and APNS_KEY_FILE are read from config.env. The key is never logged, returned, or copied:
// every way of printing a Key shows its key id only, and no error text carries file content (not even the file name
// config.env asked for).
package apnskey

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	envFile     = "config.env"
	maxFileSize = 64 << 10 // a key or an env file is a few hundred bytes; refuse anything absurd
)

// Key is a loaded APNs auth key. Private is the signing key; treat the whole value as secret.
type Key struct {
	KeyID   string
	TeamID  string
	Private *ecdsa.PrivateKey
}

// String is the only text form of a Key: its key id.
func (k Key) String() string { return "apnskey(" + k.KeyID + ")" }

// GoString and Format make %v / %+v / %#v / %s / %q print String() instead of the fields.
func (k Key) GoString() string           { return k.String() }
func (k Key) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, k.String()) }

// Load reads the key from dir (already expanded; no "~").
func Load(dir string) (Key, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return Key{}, errors.New("apns directory cannot be opened")
	}
	defer root.Close()

	env, err := readThrough(root, envFile)
	if err != nil {
		return Key{}, errors.New("apns config.env cannot be read")
	}
	vals := parseEnv(string(env))
	keyID, teamID, keyFile := vals["APNS_KEY_ID"], vals["APNS_TEAM_ID"], vals["APNS_KEY_FILE"]
	if keyID == "" || teamID == "" || keyFile == "" {
		return Key{}, errors.New("apns config.env must set APNS_KEY_ID, APNS_TEAM_ID and APNS_KEY_FILE")
	}

	raw, err := readThrough(root, keyFile)
	if err != nil {
		return Key{}, errors.New("apns key file cannot be read (it must be a name inside the directory)")
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return Key{}, errors.New("apns key file is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return Key{}, errors.New("apns key file is not a PKCS#8 private key")
	}
	priv, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || priv.Curve != elliptic.P256() {
		return Key{}, errors.New("apns key must be an EC P-256 key")
	}
	return Key{KeyID: keyID, TeamID: teamID, Private: priv}, nil
}

func readThrough(root *os.Root, name string) ([]byte, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFileSize {
		return nil, errors.New("file too large")
	}
	return data, nil
}

// parseEnv reads KEY=value lines: blank lines and # comments skipped, an optional `export `, optional single or double
// quotes around the value, whitespace around the key and the value trimmed.
func parseEnv(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		k, v, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		switch k {
		case "APNS_KEY_ID", "APNS_TEAM_ID", "APNS_KEY_FILE":
			out[k] = v
		}
	}
	return out
}
