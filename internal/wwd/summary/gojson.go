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
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev goEvent
		if json.Unmarshal(line, &ev) != nil || ev.Action == "" {
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
			if b.Len() < 4*maxMessageBytes {
				b.WriteString(ev.Output)
			}
		case "pass":
			if ev.Test != "" {
				s.Passed++
			}
		case "skip":
			if ev.Test != "" {
				s.Skipped++
			}
		case "fail":
			if ev.Test != "" {
				s.Failed++
				failedTests[ev.Package] = true
				failedOrder = append(failedOrder, k)
			} else {
				pkgFailed = append(pkgFailed, ev.Package)
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
		if failedTests[pkg] {
			continue // already told by its tests
		}
		s.Failed++
		addFailure(&s, wire.Failure{Name: pkg, Message: testMessage(output[key{pkg, ""}])})
	}
	return s, nil
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
