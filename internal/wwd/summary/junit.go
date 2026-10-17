package summary

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"github.com/wake/purdex/internal/wwd/wire"
)

// maxJunitDepth is how deep <testsuite> elements may nest; real reports stay in single digits.
const maxJunitDepth = 32

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

// parseJunit reads JUnit XML as phpunit and jest-junit write it: <testsuites> holding nested <testsuite>, or a single
// <testsuite>. A case with <failure> or <error> is failed, with <skipped> skipped, otherwise passed. The document is read as
// a token stream, so its nesting never becomes recursion; the suite depth is capped.
func parseJunit(in Input) (wire.Summary, error) {
	var s wire.Summary
	if len(bytes.TrimSpace(in.Junit)) == 0 {
		return s, fmt.Errorf("junit: the report file is empty or missing")
	}
	dec := xml.NewDecoder(bytes.NewReader(in.Junit))
	depth := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return s, nil
		}
		if err != nil {
			return s, fmt.Errorf("junit: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "testsuite", "testsuites":
				if depth++; depth > maxJunitDepth {
					return s, fmt.Errorf("junit: suites nest deeper than %d", maxJunitDepth)
				}
			case "testcase":
				var c junitCase
				if err := dec.DecodeElement(&c, &t); err != nil {
					return s, fmt.Errorf("junit: %w", err)
				}
				addCase(&s, c, in.Root)
			}
		case xml.EndElement:
			if t.Name.Local == "testsuite" || t.Name.Local == "testsuites" {
				depth--
			}
		}
	}
}

func addCase(s *wire.Summary, c junitCase, root string) {
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
		addFailure(s, wire.Failure{Name: name, File: rel(root, file), Message: msg})
	case c.Skipped != nil:
		s.Skipped++
	default:
		s.Passed++
	}
}
