package facts

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/wake/purdex/internal/peers"
)

// procargs2 layout: argc (native int32), exec path, NULs, argv strings, env strings.
func procargsBuf(execPath string, argv, env []string) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.NativeEndian, int32(len(argv)))
	b.WriteString(execPath)
	b.Write([]byte{0, 0, 0})
	for _, a := range argv {
		b.WriteString(a)
		b.WriteByte(0)
	}
	for _, e := range env {
		b.WriteString(e)
		b.WriteByte(0)
	}
	return b.Bytes()
}

func TestParseProcArgs(t *testing.T) {
	buf := procargsBuf("/usr/bin/node", []string{"node", "/x/vite", "--port", "5174"}, []string{"HOME=/h", "WWD_JOB=j_9", "A="})
	args, env, ok := parseProcArgs(buf)
	if !ok || !reflect.DeepEqual(args, []string{"node", "/x/vite", "--port", "5174"}) {
		t.Fatalf("args %v ok=%v", args, ok)
	}
	if !reflect.DeepEqual(env, []string{"HOME=/h", "WWD_JOB=j_9", "A="}) {
		t.Fatalf("env %v", env)
	}
	if id, ok := JobFromEnv(env); !ok || id != "j_9" {
		t.Fatalf("job %q %v", id, ok)
	}
	// no environment follows the argv
	args, env, ok = parseProcArgs(procargsBuf("/bin/sleep", []string{"sleep", "1"}, nil))
	if !ok || len(args) != 2 || len(env) != 0 {
		t.Fatalf("%v %v %v", args, env, ok)
	}
	// corrupt buffers are refused, never a panic
	for _, bad := range [][]byte{nil, {1, 0, 0}, {5, 0, 0, 0, 'a', 0}, {1, 0, 0, 0}} {
		if _, _, ok := parseProcArgs(bad); ok {
			t.Errorf("%v accepted", bad)
		}
	}
}

func TestSessionRefParity(t *testing.T) {
	for _, id := range []string{"cc6a5dad-c256-4c83-bb33-4839d02851cf", "x", "0000"} {
		if got, want := sessionRef(id), peers.RefID(id); got != want {
			t.Errorf("sessionRef(%q) = %q, peers.RefID = %q", id, got, want)
		}
	}
}

func TestSessionDirScan(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("100.json", `{"pid":100,"sessionId":"sid-a","procStart":"Sat Oct 17 04:34:23 2026"}`)
	write("101.json", `{"pid":101,"sessionId":"sid-b","procStart":"Sat Oct  3 01:02:03 2026"}`)
	write("102.json", `{"pid":999,"sessionId":"sid-c"}`) // file name and pid disagree: ignored
	write("junk.json", `not json`)
	write("100.abc.key", `secret`) // other files are never read
	got := scanSessions(dir)
	if len(got) != 2 || got[100].sessionID != "sid-a" || got[101].sessionID != "sid-b" {
		t.Fatalf("%+v", got)
	}
	// procStart must equal the process's start time (a recycled pid is not the session)
	at := time.Date(2026, 10, 17, 4, 34, 23, 0, time.Local)
	if !got[100].matchesStart(at.Unix()) {
		t.Error("same start refused")
	}
	if got[100].matchesStart(at.Unix() + 60) {
		t.Error("different start accepted")
	}
	at3 := time.Date(2026, 10, 3, 1, 2, 3, 0, time.Local) // single-digit day is space padded
	if !got[101].matchesStart(at3.Unix()) {
		t.Error("space-padded day refused")
	}
}
