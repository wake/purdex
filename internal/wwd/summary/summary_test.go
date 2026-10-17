package summary

import (
	"fmt"
	"strings"
	"testing"
)

const goPass = `{"Action":"run","Package":"p/a","Test":"TestOne"}
{"Action":"output","Package":"p/a","Test":"TestOne","Output":"=== RUN   TestOne\n"}
{"Action":"pass","Package":"p/a","Test":"TestOne"}
{"Action":"run","Package":"p/a","Test":"TestSkip"}
{"Action":"skip","Package":"p/a","Test":"TestSkip"}
{"Action":"pass","Package":"p/a"}
`

const goFail = `{"Action":"run","Package":"p/b","Test":"TestBad"}
{"Action":"output","Package":"p/b","Test":"TestBad","Output":"=== RUN   TestBad\n"}
{"Action":"output","Package":"p/b","Test":"TestBad","Output":"    bad_test.go:12: want 1 got 2\n"}
{"Action":"output","Package":"p/b","Test":"TestBad","Output":"--- FAIL: TestBad (0.00s)\n"}
{"Action":"fail","Package":"p/b","Test":"TestBad"}
{"Action":"output","Package":"p/b","Output":"FAIL\n"}
{"Action":"fail","Package":"p/b"}
{"Action":"run","Package":"p/c","Test":"TestFine"}
{"Action":"pass","Package":"p/c","Test":"TestFine"}
{"Action":"pass","Package":"p/c"}
`

const goBuildFail = `{"Action":"output","Package":"p/d","Output":"# p/d\n"}
{"Action":"output","Package":"p/d","Output":"d.go:3:2: undefined: x\n"}
{"Action":"fail","Package":"p/d"}
`

func TestGoJSON(t *testing.T) {
	s, err := Parse("go-json", Input{Stdout: []byte(goPass + goFail + goBuildFail), Log: []byte("l1\nl2\n")})
	if err != nil {
		t.Fatal(err)
	}
	if s.Passed != 2 || s.Skipped != 1 || s.Failed != 2 {
		t.Fatalf("counts = %d/%d/%d", s.Passed, s.Failed, s.Skipped)
	}
	if len(s.Failures) != 2 {
		t.Fatalf("failures = %+v", s.Failures)
	}
	f := s.Failures[0]
	if f.Name != "p/b.TestBad" || f.File != "bad_test.go" || !strings.Contains(f.Message, "want 1 got 2") || strings.Contains(f.Message, "=== RUN") {
		t.Fatalf("failure = %+v", f)
	}
	b := s.Failures[1]
	if b.Name != "p/d" || !strings.Contains(b.Message, "undefined: x") {
		t.Fatalf("build failure = %+v", b)
	}
	if len(s.Tail) != 2 || s.Tail[1] != "l2" {
		t.Fatalf("tail = %v", s.Tail)
	}
}

func TestGoJSONTruncatedEventIsAnError(t *testing.T) {
	stdout := `{"Action":"run","Package":"p","Test":"T"}` + "\n" + `{"Action":"fail","Package":"p","Te`
	s, err := Parse("go-json", Input{Stdout: []byte(stdout)})
	if err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("err = %v, want a malformed-event error (summary %+v)", err, s)
	}
}

func TestGoJSONNoEventsIsAnError(t *testing.T) {
	s, err := Parse("go-json", Input{Stdout: []byte("not json\nanother line\n"), Log: []byte("boom\n")})
	if err == nil {
		t.Fatal("want an error for output without test events")
	}
	if s.Failed != 0 || len(s.Tail) != 1 || s.Failures == nil {
		t.Fatalf("summary must stay usable: %+v", s)
	}
}

const vitestSample = `{"numTotalTests":4,"testResults":[
 {"name":"/ws/spa/src/a.test.ts","status":"failed","assertionResults":[
   {"fullName":"a works","status":"passed"},
   {"fullName":"a breaks","status":"failed","failureMessages":["expected 1 to be 2","at a.test.ts:9"]},
   {"fullName":"a later","status":"pending"}]},
 {"name":"/ws/spa/src/b.test.ts","status":"failed","message":"Cannot find module './x'","assertionResults":[]},
 {"name":"/ws/spa/src/c.test.ts","status":"passed","assertionResults":[{"fullName":"c ok","status":"passed"}]}]}`

func TestVitest(t *testing.T) {
	s, err := Parse("vitest-json", Input{Report: []byte(vitestSample), Root: "/ws"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Passed != 2 || s.Failed != 2 || s.Skipped != 1 {
		t.Fatalf("counts = %d/%d/%d", s.Passed, s.Failed, s.Skipped)
	}
	if s.Failures[0].Name != "a breaks" || s.Failures[0].File != "spa/src/a.test.ts" || !strings.Contains(s.Failures[0].Message, "expected 1 to be 2") {
		t.Fatalf("failure 0 = %+v", s.Failures[0])
	}
	if s.Failures[1].Name != "spa/src/b.test.ts" || s.Failures[1].Message != "Cannot find module './x'" {
		t.Fatalf("suite failure = %+v", s.Failures[1])
	}
}

func TestVitestMissingReport(t *testing.T) {
	s, err := Parse("vitest-json", Input{Log: []byte("crashed\n")})
	if err == nil || len(s.Tail) != 1 {
		t.Fatalf("err %v, summary %+v", err, s)
	}
	if _, err := Parse("vitest-json", Input{Report: []byte("{not json")}); err == nil {
		t.Fatal("want a decode error")
	}
}

const junitSuites = `<?xml version="1.0"?>
<testsuites>
 <testsuite name="outer">
  <testsuite name="inner">
   <testcase name="testA" classname="Tests\FooTest" file="tests/FooTest.php"/>
   <testcase name="testB" classname="Tests\FooTest" file="tests/FooTest.php"><failure message="expected true">trace line</failure></testcase>
  </testsuite>
  <testcase name="testC" classname="Tests\BarTest"><error message="boom"/></testcase>
  <testcase name="testD" classname="Tests\BarTest"><skipped/></testcase>
 </testsuite>
</testsuites>`

func TestJunit(t *testing.T) {
	s, err := Parse("junit", Input{Junit: []byte(junitSuites)})
	if err != nil {
		t.Fatal(err)
	}
	if s.Passed != 1 || s.Failed != 2 || s.Skipped != 1 {
		t.Fatalf("counts = %d/%d/%d", s.Passed, s.Failed, s.Skipped)
	}
	f := s.Failures[0]
	if f.Name != `Tests\FooTest::testB` || f.File != "tests/FooTest.php" || f.Message != "expected true\ntrace line" {
		t.Fatalf("failure = %+v", f)
	}
	if s.Failures[1].File != `Tests\BarTest` { // no file attribute: the class stands in
		t.Fatalf("failure 1 = %+v", s.Failures[1])
	}
}

func TestJunitBareSuiteRoot(t *testing.T) {
	s, err := Parse("junit", Input{Junit: []byte(`<testsuite><testcase name="a"/><testcase name="b"><failure message="x"/></testcase></testsuite>`)})
	if err != nil || s.Passed != 1 || s.Failed != 1 {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestPlainKeepsTheLast40Lines(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	s, err := Parse("plain", Input{Log: []byte(b.String())})
	if err != nil || len(s.Tail) != 40 || s.Tail[0] != "line 61" || s.Tail[39] != "line 100" {
		t.Fatalf("tail = %v err %v", s.Tail, err)
	}
	if s.Passed != 0 || s.Failed != 0 || s.Failures == nil {
		t.Fatalf("plain has no counts: %+v", s)
	}
}

func TestLongLinesAndManyFailuresAreBounded(t *testing.T) {
	s, _ := Parse("plain", Input{Log: []byte(strings.Repeat("é", 1000) + "\n")})
	if len(s.Tail[0]) > maxLineBytes+4 {
		t.Fatalf("line not clipped: %d", len(s.Tail[0]))
	}
	for _, r := range s.Tail[0] {
		if r == '�' {
			t.Fatal("clipped inside a UTF-8 sequence")
		}
	}
	var ev strings.Builder
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&ev, `{"Action":"fail","Package":"p","Test":"T%d"}`+"\n", i)
	}
	g, _ := Parse("go-json", Input{Stdout: []byte(ev.String())})
	if g.Failed != 80 || len(g.Failures) != maxFailures {
		t.Fatalf("failed %d, listed %d", g.Failed, len(g.Failures))
	}
}

func TestPathsOutsideTheWorkspaceAreNotRepeated(t *testing.T) {
	report := `{"testResults":[{"name":"/home/secret/proj/x.test.ts","status":"failed","assertionResults":[{"fullName":"x","status":"failed","failureMessages":["m"]}]},
	 {"name":"../../etc/y.test.ts","status":"failed","assertionResults":[{"fullName":"y","status":"failed"}]}]}`
	s, err := Parse("vitest-json", Input{Report: []byte(report), Root: "/ws"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Failures[0].File != "<outside-workspace>/x.test.ts" || s.Failures[1].File != "<outside-workspace>/y.test.ts" {
		t.Fatalf("files = %q, %q", s.Failures[0].File, s.Failures[1].File)
	}
	if got := rel("/ws", "/ws/a/b.ts"); got != "a/b.ts" {
		t.Fatalf("inside path = %q", got)
	}
	if got := rel("/ws", "/ws2/a.ts"); got != "<outside-workspace>/a.ts" {
		t.Fatalf("sibling dir = %q", got)
	}
}

func TestVitestUnknownStatusIsAnError(t *testing.T) {
	report := `{"testResults":[{"name":"a","status":"passed","assertionResults":[{"fullName":"x","status":"passd"},{"fullName":"y","status":"passed"}]}]}`
	s, err := Parse("vitest-json", Input{Report: []byte(report)})
	if err == nil || !strings.Contains(err.Error(), "passd") {
		t.Fatalf("err = %v", err)
	}
	if s.Passed != 1 || s.Skipped != 0 {
		t.Fatalf("an unknown status must not count as skipped: %+v", s)
	}
}

func TestJunitDeepNestingIsRefusedWithoutRecursion(t *testing.T) {
	deep := strings.Repeat("<testsuite>", 200000) + `<testcase name="a"/>` + strings.Repeat("</testsuite>", 200000)
	if _, err := Parse("junit", Input{Junit: []byte(deep)}); err == nil || !strings.Contains(err.Error(), "nest") {
		t.Fatalf("err = %v", err)
	}
}

func TestJunitMalformedIsAnError(t *testing.T) {
	if _, err := Parse("junit", Input{Junit: []byte(`<testsuite><testcase name="a">`)}); err == nil {
		t.Fatal("want an error for an unterminated document")
	}
}

func TestOversizedReportIsRefused(t *testing.T) {
	big := make([]byte, maxInput+1)
	for _, kind := range []string{"go-json", "vitest-json", "junit"} {
		in := Input{Stdout: big, Report: big, Junit: big, Log: []byte("tail\n")}
		s, err := Parse(kind, in)
		if err == nil || len(s.Tail) != 1 {
			t.Errorf("%s: err %v summary %+v", kind, err, s)
		}
	}
}

func TestGoJSONKeepsOnlyABoundedMessageAndNothingForPassedTests(t *testing.T) {
	var b strings.Builder
	huge := strings.Repeat("x", 1<<20)
	fmt.Fprintf(&b, `{"Action":"output","Package":"p","Test":"TestPass","Output":%q}`+"\n", huge)
	b.WriteString(`{"Action":"pass","Package":"p","Test":"TestPass"}` + "\n")
	fmt.Fprintf(&b, `{"Action":"output","Package":"p","Test":"TestBad","Output":%q}`+"\n", huge)
	b.WriteString(`{"Action":"fail","Package":"p","Test":"TestBad"}` + "\n")
	s, err := Parse("go-json", Input{Stdout: []byte(b.String())})
	if err != nil {
		t.Fatal(err)
	}
	if s.Passed != 1 || s.Failed != 1 || len(s.Failures[0].Message) > maxMessageBytes+4 {
		t.Fatalf("summary = passed %d failed %d, message %d bytes", s.Passed, s.Failed, len(s.Failures[0].Message))
	}
}

func TestGoJSONPackageFailureWithFailedTestsIsNotCountedTwice(t *testing.T) {
	stdout := `{"Action":"fail","Package":"p","Test":"T"}` + "\n" + `{"Action":"fail","Package":"p"}` + "\n"
	s, err := Parse("go-json", Input{Stdout: []byte(stdout)})
	if err != nil || s.Failed != 1 || len(s.Failures) != 1 {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestLastLinesEdges(t *testing.T) {
	cases := map[string][]string{
		"":              {},
		"one":           {"one"},
		"a\nb\nc\n":     {"a", "b", "c"},
		"\nlead\nb\n":   {"lead", "b"},
		"a\r\nb\r\n":    {"a", "b"},
		"1\n2\n3\n4\n5": {"3", "4", "5"},
	}
	for in, want := range cases {
		n := 3
		got := lastLines([]byte(in), n)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("lastLines(%q, %d) = %q, want %q", in, n, got, want)
		}
	}
}

func TestUnknownKind(t *testing.T) {
	s, err := Parse("xml", Input{Log: []byte("x\n")})
	if err == nil || len(s.Tail) != 1 {
		t.Fatalf("%+v %v", s, err)
	}
}
