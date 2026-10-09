package apnskey

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A throwaway key for every test: nothing here reads a real key directory.
func pemOf(t *testing.T, key any) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func p256(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func write(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

const goodEnv = "APNS_KEY_ID=KEYID12345\nAPNS_TEAM_ID=TEAMID6789\nAPNS_KEY_FILE=AuthKey_KEYID12345.p8\n"

func keyDir(t *testing.T, env string, key any) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "config.env", []byte(env))
	if key != nil {
		write(t, dir, "AuthKey_KEYID12345.p8", pemOf(t, key))
	}
	return dir
}

func TestLoad_HappyPath(t *testing.T) {
	want := p256(t)
	k, err := Load(keyDir(t, goodEnv, want))
	if err != nil {
		t.Fatal(err)
	}
	if k.KeyID != "KEYID12345" || k.TeamID != "TEAMID6789" {
		t.Fatalf("ids = %q / %q", k.KeyID, k.TeamID)
	}
	if k.Private == nil || !k.Private.Equal(want) {
		t.Fatal("the loaded key is not the key in the file")
	}
}

func TestLoad_CommentsQuotesExportAndOtherKeysAreHandled(t *testing.T) {
	env := "# the push key\n\nexport APNS_KEY_ID=\"KEYID12345\"\nAPNS_TEAM_ID='TEAMID6789'\nSOMETHING_ELSE=not-a-secret\nAPNS_KEY_FILE = AuthKey_KEYID12345.p8 \n  # trailing\n"
	k, err := Load(keyDir(t, env, p256(t)))
	if err != nil {
		t.Fatal(err)
	}
	if k.KeyID != "KEYID12345" || k.TeamID != "TEAMID6789" {
		t.Fatalf("ids = %q / %q", k.KeyID, k.TeamID)
	}
}

func TestLoad_MissingPiecesAreErrors(t *testing.T) {
	for name, env := range map[string]string{
		"no key id":   "APNS_TEAM_ID=T\nAPNS_KEY_FILE=AuthKey_KEYID12345.p8\n",
		"no team id":  "APNS_KEY_ID=K\nAPNS_KEY_FILE=AuthKey_KEYID12345.p8\n",
		"no key file": "APNS_KEY_ID=K\nAPNS_TEAM_ID=T\n",
		"empty":       "",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(keyDir(t, env, p256(t))); err == nil {
				t.Fatal("want an error")
			}
		})
	}
	if _, err := Load(t.TempDir()); err == nil { // no config.env at all
		t.Fatal("a directory without config.env must fail")
	}
	if _, err := Load(keyDir(t, goodEnv, nil)); err == nil { // config.env names a file that is not there
		t.Fatal("a missing key file must fail")
	}
	if _, err := Load(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("a missing directory must fail")
	}
}

func TestLoad_OnlyAnEcdsaP256PKCS8KeyIsAccepted(t *testing.T) {
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	for name, key := range map[string]any{"wrong curve": p384, "rsa": rsaKey} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(keyDir(t, goodEnv, key)); err == nil {
				t.Fatal("want an error")
			}
		})
	}
	dir := keyDir(t, goodEnv, nil)
	write(t, dir, "AuthKey_KEYID12345.p8", []byte("this is not a pem\n"))
	if _, err := Load(dir); err == nil {
		t.Fatal("garbage must fail")
	}
}

// The key file name is relative to the directory and cannot leave it: absolute, "..", and a symlink out all fail.
func TestLoad_TheKeyFileCannotLeaveTheDirectory(t *testing.T) {
	outside := t.TempDir()
	write(t, outside, "stolen.p8", pemOf(t, p256(t)))

	absolute := keyDir(t, "APNS_KEY_ID=K\nAPNS_TEAM_ID=T\nAPNS_KEY_FILE="+filepath.Join(outside, "stolen.p8")+"\n", nil)
	if _, err := Load(absolute); err == nil {
		t.Fatal("an absolute key file path must fail")
	}

	dotdot := keyDir(t, "APNS_KEY_ID=K\nAPNS_TEAM_ID=T\nAPNS_KEY_FILE=../"+filepath.Base(outside)+"/stolen.p8\n", nil)
	if _, err := Load(dotdot); err == nil {
		t.Fatal(".. in the key file name must fail")
	}

	link := keyDir(t, "APNS_KEY_ID=K\nAPNS_TEAM_ID=T\nAPNS_KEY_FILE=link.p8\n", nil)
	if err := os.Symlink(filepath.Join(outside, "stolen.p8"), filepath.Join(link, "link.p8")); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(link); err == nil {
		t.Fatal("a symlink that leaves the directory must fail")
	}

	// a symlink that stays inside is fine
	inside := keyDir(t, "APNS_KEY_ID=K\nAPNS_TEAM_ID=T\nAPNS_KEY_FILE=alias.p8\n", p256(t))
	if err := os.Symlink("AuthKey_KEYID12345.p8", filepath.Join(inside, "alias.p8")); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(inside); err != nil {
		t.Fatalf("a symlink inside the directory must work: %v", err)
	}
}

func TestLoad_ConfigEnvItselfCannotBeASymlinkOut(t *testing.T) {
	outside := t.TempDir()
	write(t, outside, "config.env", []byte(goodEnv))
	dir := t.TempDir()
	write(t, dir, "AuthKey_KEYID12345.p8", pemOf(t, p256(t)))
	if err := os.Symlink(filepath.Join(outside, "config.env"), filepath.Join(dir, "config.env")); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("a config.env that is a symlink out of the directory must fail")
	}
}

// Nothing printed about a key, or about a failure to load one, carries file content or key material.
func TestNoPrintingExposesKeyMaterialOrFileContent(t *testing.T) {
	secret := "SUPERSECRETVALUE0123456789"
	k, err := Load(keyDir(t, goodEnv, p256(t)))
	if err != nil {
		t.Fatal(err)
	}
	if got := k.String(); got != "apnskey(KEYID12345)" {
		t.Fatalf("String = %q", got)
	}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		out := fmt.Sprintf(verb, k)
		if strings.Contains(out, "TEAMID6789") || strings.Contains(out, "PRIVATE") || strings.Contains(out, "X:") || strings.Contains(out, "D:") {
			t.Fatalf("%s leaks: %s", verb, out)
		}
	}
	// error texts: put a secret in every file and make each fail
	dir := t.TempDir()
	write(t, dir, "config.env", []byte("APNS_KEY_ID="+secret+"\nAPNS_TEAM_ID="+secret+"\nAPNS_KEY_FILE=k.p8\n"))
	write(t, dir, "k.p8", []byte(secret+"\n-----BEGIN PRIVATE KEY-----\n"+secret+"\n-----END PRIVATE KEY-----\n"))
	_, err = Load(dir)
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error text carries file content: %v", err)
	}
	bad := t.TempDir()
	write(t, bad, "config.env", []byte("APNS_KEY_ID=K\nAPNS_TEAM_ID=T\nAPNS_KEY_FILE=../"+secret+"\n"))
	if _, err = Load(bad); err == nil {
		t.Fatal("want an error")
	} else if strings.Contains(err.Error(), secret) { // not even the file NAME it was told to open is echoed
		t.Fatalf("error text carries file content: %v", err)
	}
}

// A FIFO (or any non-regular file) in place of config.env or the key must fail at once, not block the daemon's boot.
func TestLoad_ASpecialFileFailsFastInsteadOfBlocking(t *testing.T) {
	for name, build := range map[string]func(dir string){
		"config.env is a fifo": func(dir string) {
			if err := syscall.Mkfifo(filepath.Join(dir, "config.env"), 0o600); err != nil {
				t.Fatal(err)
			}
			write(t, dir, "AuthKey_KEYID12345.p8", pemOf(t, p256(t)))
		},
		"key file is a fifo": func(dir string) {
			write(t, dir, "config.env", []byte(goodEnv))
			if err := syscall.Mkfifo(filepath.Join(dir, "AuthKey_KEYID12345.p8"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"key file is a directory": func(dir string) {
			write(t, dir, "config.env", []byte(goodEnv))
			if err := os.Mkdir(filepath.Join(dir, "AuthKey_KEYID12345.p8"), 0o700); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			build(dir)
			done := make(chan error, 1)
			go func() { _, err := Load(dir); done <- err }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("want an error")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("Load blocked on a special file")
			}
		})
	}
}
