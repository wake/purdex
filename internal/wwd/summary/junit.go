package summary

import (
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/wake/purdex/internal/wwd/wire"
)

type junitProblem struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	File      string        `xml:"file,attr"`
	Failure   *junitProblem `xml:"failure"`
	Error     *junitProblem `xml:"error"`
	Skipped   *struct{}     `xml:"skipped"`
}

type junitSuite struct {
	Cases  []junitCase  `xml:"testcase"`
	Suites []junitSuite `xml:"testsuite"`
}

type junitRoot struct {
	Suites []junitSuite `xml:"testsuite"`
	Cases  []junitCase  `xml:"testcase"` // a bare <testsuite> root decodes into Cases of its own
}

// parseJunit reads JUnit XML as phpunit and jest-junit write it: <testsuites> holding nested <testsuite>, or a single
// <testsuite>. A case with <failure> or <error> is failed, with <skipped> skipped, otherwise passed.
func parseJunit(in Input) (wire.Summary, error) {
	var s wire.Summary
	if len(strings.TrimSpace(string(in.Junit))) == 0 {
		return s, fmt.Errorf("junit: the report file is empty or missing")
	}
	var root junitRoot
	if err := xml.Unmarshal(in.Junit, &root); err != nil {
		return s, fmt.Errorf("junit: %w", err)
	}
	var walk func(cases []junitCase, suites []junitSuite)
	walk = func(cases []junitCase, suites []junitSuite) {
		for _, c := range cases {
			p := c.Failure
			if p == nil {
				p = c.Error
			}
			switch {
			case p != nil:
				s.Failed++
				msg := p.Message
				if t := strings.TrimSpace(p.Text); t != "" {
					msg = strings.TrimSpace(msg + "\n" + t)
				}
				name := c.Name
				if c.Classname != "" {
					name = c.Classname + "::" + c.Name
				}
				file := c.File
				if file == "" {
					file = c.Classname
				}
				addFailure(&s, wire.Failure{Name: name, File: rel(in.Root, file), Message: msg})
			case c.Skipped != nil:
				s.Skipped++
			default:
				s.Passed++
			}
		}
		for _, st := range suites {
			walk(st.Cases, st.Suites)
		}
	}
	walk(root.Cases, root.Suites)
	return s, nil
}
