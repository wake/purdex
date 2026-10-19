package summary

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/wake/purdex/internal/wwd/wire"
)

type goEvent struct {
	Action  string
	Package string
	Test    string
	Output  string
}

// maxTestOutput is how much of one test's output is kept while the stream is read.
const maxTestOutput = 8 << 10

var goFileRe = regexp.MustCompile(`([\w./-]+\.go):\d+`)

// parseGoJSON reads `go test -json` (test2json) output. Each test that ended counts once, subtests included; a package that
// failed without a failed test (a build error) is a failure of its own, with the package's output as the message.
func parseGoJSON(in Input) (wire.Summary, error) {
	var s wire.Summary
	type key struct{ pkg, test string }
	output := map[key]*strings.Builder{}
	var failedOrder []key
	failedTests := map[string]bool{} // packages that have a failed test
	var pkgFailed []string
	events := 0
	sc := bufio.NewScanner(bytes.NewReader(in.Stdout))
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	var malformed error
	passed := newPassedSet(in.Digest)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev goEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			// a line that looks like an event but is cut or corrupted: the counts cannot be trusted
			if malformed == nil {
				malformed = fmt.Errorf("go-json: malformed event at line %d: %v", lineNo, err)
			}
			continue
		}
		if ev.Action == "" {
			continue
		}
		events++
		k := key{ev.Package, ev.Test}
		switch ev.Action {
		case "output":
			b := output[k]
			if b == nil {
				b = &strings.Builder{}
				output[k] = b
			}
			if room := maxTestOutput - b.Len(); room > 0 {
				o := ev.Output
				if len(o) > room {
					o = o[:room]
				}
				b.WriteString(o)
			}
		case "pass":
			delete(output, k) // a test that passed needs no output kept
			if ev.Test != "" {
				s.Passed++
				passed.add(ev.Package, ev.Test)
			}
		case "skip":
			delete(output, k)
			if ev.Test != "" {
				s.Skipped++
				passed.notPassed(ev.Package, ev.Test)
			}
		case "fail":
			if ev.Test != "" {
				s.Failed++
				passed.notPassed(ev.Package, ev.Test)
				failedTests[ev.Package] = true
				if len(failedOrder) < maxFailures {
					failedOrder = append(failedOrder, k)
				} else {
					delete(output, k)
				}
			} else if !failedTests[ev.Package] { // its tests already told; a package that failed alone is a build or setup failure
				s.Failed++
				if len(pkgFailed) < maxFailures {
					pkgFailed = append(pkgFailed, ev.Package)
				} else {
					delete(output, k)
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return s, fmt.Errorf("go-json: %w", err)
	}
	if events == 0 {
		return s, fmt.Errorf("go-json: no test events in the output")
	}
	for _, k := range failedOrder {
		msg := testMessage(output[k])
		f := wire.Failure{Name: k.pkg + "." + k.test, Message: msg}
		if m := goFileRe.FindStringSubmatch(msg); m != nil {
			f.File = rel(in.Root, m[1])
		}
		addFailure(&s, f)
	}
	for _, pkg := range pkgFailed {
		addFailure(&s, wire.Failure{Name: pkg, Message: testMessage(output[key{pkg, ""}])})
	}
	if malformed == nil { // a stream with a damaged event may have lost passes: the set is not trusted
		s.PassedDigest = passed.digest()
	}
	return s, malformed
}

// testMessage is a test's output without the runner's own bookkeeping lines.
func testMessage(b *strings.Builder) string {
	if b == nil {
		return ""
	}
	var keep []string
	for _, l := range strings.Split(b.String(), "\n") {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "=== RUN") || strings.HasPrefix(t, "=== PAUSE") || strings.HasPrefix(t, "=== CONT") ||
			strings.HasPrefix(t, "--- FAIL") || strings.HasPrefix(t, "--- PASS") || t == "FAIL" || t == "PASS" {
			continue
		}
		keep = append(keep, t)
	}
	return strings.Join(keep, "\n")
}
