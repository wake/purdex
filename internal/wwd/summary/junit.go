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
	passed := newPassedSet(in.Digest)
	var suites []string // the names of the <testsuite> elements around the token: a test is told apart by where it sits
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			s.PassedDigest = passed.digest()
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
				if t.Name.Local == "testsuite" {
					suites = append(suites, suiteName(t))
				}
			case "testcase":
				var c junitCase
				if err := dec.DecodeElement(&c, &t); err != nil {
					return s, fmt.Errorf("junit: %w", err)
				}
				addCase(&s, c, in.Root, passed, strings.Join(suites, "\x1f"))
			}
		case xml.EndElement:
			if t.Name.Local == "testsuite" || t.Name.Local == "testsuites" {
				depth--
			}
			if t.Name.Local == "testsuite" && len(suites) > 0 {
				suites = suites[:len(suites)-1]
			}
		}
	}
}

// suiteName is the name attribute of a <testsuite> ("" when it has none).
func suiteName(t xml.StartElement) string {
	for _, a := range t.Attr {
		if a.Name.Local == "name" {
			return a.Value
		}
	}
	return ""
}

// addCase counts one test case. suite is the names of the suites around it, outermost first: with the case's file, class and name they
// are the identity of a passed test for the digest, so the same class and name in two suites are two tests. A test with no name, or a
// file outside the workspace (which rel folds to its base name), cannot be told apart from another: the report then has no digest.
func addCase(s *wire.Summary, c junitCase, root string, passed *passedSet, suite string) {
	p := c.Failure
	if p == nil {
		p = c.Error
	}
	idFile := ""
	if c.File != "" {
		idFile = rel(root, c.File)
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
		passed.notPassed(suite, idFile, c.Classname, c.Name)
		addFailure(s, wire.Failure{Name: name, File: rel(root, file), Message: msg})
	case c.Skipped != nil:
		s.Skipped++
		passed.notPassed(suite, idFile, c.Classname, c.Name)
	default:
		s.Passed++
		if c.Name == "" || strings.HasPrefix(idFile, outside) {
			passed.taint()
		}
		passed.add(suite, idFile, c.Classname, c.Name)
	}
}
