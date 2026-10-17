package summary

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wake/purdex/internal/wwd/wire"
)

type vitestReport struct {
	TestResults []struct {
		Name             string `json:"name"`
		Status           string `json:"status"`
		Message          string `json:"message"`
		AssertionResults []struct {
			FullName        string   `json:"fullName"`
			Title           string   `json:"title"`
			Status          string   `json:"status"`
			FailureMessages []string `json:"failureMessages"`
		} `json:"assertionResults"`
	} `json:"testResults"`
}

// parseVitest reads vitest's JSON reporter (jest-compatible). A suite that failed without any failed test (an import error,
// a setup crash) counts as one failure named after its file.
func parseVitest(in Input) (wire.Summary, error) {
	var s wire.Summary
	if len(strings.TrimSpace(string(in.Report))) == 0 {
		return s, fmt.Errorf("vitest-json: the report file is empty or missing")
	}
	var r vitestReport
	if err := json.Unmarshal(in.Report, &r); err != nil {
		return s, fmt.Errorf("vitest-json: %w", err)
	}
	var unknown error
	for _, suite := range r.TestResults {
		file := rel(in.Root, suite.Name)
		suiteFailed := false
		for _, a := range suite.AssertionResults {
			switch a.Status {
			case "passed":
				s.Passed++
			case "failed":
				s.Failed++
				suiteFailed = true
				name := a.FullName
				if name == "" {
					name = a.Title
				}
				addFailure(&s, wire.Failure{Name: name, File: file, Message: strings.Join(a.FailureMessages, "\n")})
			case "pending", "skipped", "todo", "disabled":
				s.Skipped++
			default:
				// schema drift or a damaged report: counting it as skipped would look trustworthy and be wrong
				if unknown == nil {
					unknown = fmt.Errorf("vitest-json: unknown test status %q in %s", a.Status, file)
				}
			}
		}
		if !suiteFailed && suite.Status == "failed" {
			s.Failed++
			addFailure(&s, wire.Failure{Name: file, File: file, Message: suite.Message})
		}
	}
	return s, unknown
}
