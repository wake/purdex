// Package scrub turns a raw Claude Code transcript into a golden-fixture
// input (testdata/conversation/v1): it keeps the rows and fields the
// normalizer reads (ccnorm.ReadFields) and rewrites or removes everything
// that identifies a person, a machine or a secret. The output is
// deterministic (sorted keys, no HTML escaping) and scrubbing it again changes
// nothing.
//
// What is rewritten, in this order, in every kept string:
//
//   - the recorded cwd and session ids → /work/fixture and FixtureSessionID
//   - pdx peer addresses <host>/<name> (host mlab, air26, air19, air-2026,
//     air-2019) → <host>/fixture-peer; uds:<path>/<digits>.sock →
//     uds:/work/tmp/cc-socks/1.sock
//   - e-mail addresses → user@example.com
//   - .claude/projects/<encoded dir>/… → .claude/projects/-work-fixture/…, any
//     uuid after it → FixtureSessionID (the encoded dir names the recording
//     machine, project and session)
//   - mcp__<server>__<tool> → mcp__server__tool (the server names are the
//     recording host's setup); a bare mcp__<server> → mcp__server; the
//     server cell of a /context table row "| mcp__… | <server> | n |" → server
//   - paths: the home directory, /Users/<name>, /home/<name> → /work;
//     /private/tmp/claude-N/<project dir>, /private/tmp, /tmp,
//     /var/folders/xx/yyy/T → /work/tmp
//   - account names, as whole words → user
//   - 100.64.x.y (tailnet) → 192.0.2.1
//   - Bearer tokens, sk-/ghp_/gho_/xox keys, AWS key ids and any run of 32 or
//     more base64/hex characters mixing letters and digits → [redacted-…]
//
// A local-command stdout (<local-command-stdout>…</local-command-stdout>)
// longer than 1 KiB is replaced whole by a fixed text; shorter ones stay.
//
// Image data is replaced by a tiny valid PNG (so a recorded placeholder's
// `bytes` becomes small; the case README says so).
package scrub

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/wake/purdex/internal/convmodel/ccnorm"
)

// What the fixtures carry in place of the recorded identity.
const (
	FixtureCwd       = "/work/fixture"
	FixtureSessionID = "00000000-0000-4000-8000-000000000001"
	FixtureBranch    = "main"

	// FixtureProjectDir stands in for the encoded directory under
	// .claude/projects; FixtureMcpServer / FixtureMcpTool for the segments of
	// every mcp__<server>__<tool> name.
	FixtureProjectDir = "-work-fixture"
	FixtureMcpServer  = "server"
	FixtureMcpTool    = "tool"

	// FixturePeer is the name part of every pdx peer address, FixtureSocket
	// the form of every uds: peer socket.
	FixturePeer   = "fixture-peer"
	FixtureSocket = "uds:/work/tmp/cc-socks/1.sock"

	// FixtureHost stands in for the host part of a pdx address and for every
	// bare host name of the recording setup.
	FixtureHost = "host"

	// TinyPNG is a 1x1 PNG, the stand-in for every image's base64 data.
	TinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="
)

// Options names what to hide. The zero value hides the current account.
type Options struct {
	Home  string   // home directory; "" = os.UserHomeDir()
	Users []string // account names, rewritten as whole words; nil = the current account

	// AllowBadRows counts and skips a row that is not a JSON object or not
	// valid UTF-8 instead of failing the run. It does not lift a size limit.
	AllowBadRows bool

	// Limits; zero means the Default… value.
	MaxLineBytes  int
	MaxTotalBytes int
	MaxDepth      int
}

// The default limits: one line, the whole input, and the nesting depth of a row
// (the row object is depth 1).
const (
	DefaultMaxLineBytes  = 16 << 20
	DefaultMaxTotalBytes = 256 << 20
	DefaultMaxDepth      = 64
)

// RowError is a row (a line of the input) the scrubber refuses. It names the
// line and the reason, never the content.
type RowError struct {
	Line   int
	Reason string
}

func (e *RowError) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Reason) }

// Report counts what a run read and left out.
type Report struct {
	Rows, Kept int
	Dropped    map[string]int // by reason: "type:<t>", "system:<s>", "attachment:<t>", "not_json"
}

// Scrub reads a transcript from r and writes the fixture input to w.
func Scrub(r io.Reader, w io.Writer, o Options) (Report, error) {
	rep := Report{Dropped: map[string]int{}}
	maxLine, maxTotal, maxDepth := orDefault(o.MaxLineBytes, DefaultMaxLineBytes), orDefault(o.MaxTotalBytes, DefaultMaxTotalBytes), orDefault(o.MaxDepth, DefaultMaxDepth)
	var rows []map[string]any
	br := bufio.NewReaderSize(r, 1<<20)
	total := 0
	for lineNo := 1; ; lineNo++ {
		line, raw, err := readLine(br, maxLine)
		if err == errLineTooLong {
			return rep, &RowError{lineNo, fmt.Sprintf("longer than %d bytes", maxLine)}
		}
		if total += raw; total > maxTotal {
			return rep, fmt.Errorf("input is larger than %d bytes", maxTotal)
		}
		if line = bytes.TrimSpace(line); len(line) > 0 {
			rep.Rows++
			m, kind, reason := decodeRow(line)
			switch {
			case m != nil && exceedsDepth(m, 1, maxDepth):
				return rep, &RowError{lineNo, fmt.Sprintf("nested deeper than %d levels", maxDepth)}
			case m != nil:
				rows = append(rows, m)
			case o.AllowBadRows:
				rep.Dropped[kind]++
			default:
				return rep, &RowError{lineNo, reason + " (use -allow-bad-rows to count and skip such rows)"}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return rep, err
		}
	}

	rw := newRewriter(o, rows)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, m := range rows {
		out, reason := scrubRow(m, rw)
		if out == nil {
			rep.Dropped[reason]++
			continue
		}
		rep.Kept++
		if err := enc.Encode(out); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

func orDefault(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

var errLineTooLong = errors.New("line too long")

// readLine reads one line (newline included) of at most max bytes of content;
// raw is how many bytes it consumed from br. A longer line stops the read
// early with errLineTooLong, before it is held whole.
func readLine(br *bufio.Reader, max int) (line []byte, raw int, err error) {
	for {
		chunk, e := br.ReadSlice('\n')
		raw += len(chunk)
		line = append(line, chunk...)
		if len(line) > max+1 {
			return nil, raw, errLineTooLong
		}
		if e == bufio.ErrBufferFull {
			continue
		}
		content := len(line)
		if content > 0 && line[content-1] == '\n' {
			content--
		}
		if content > max {
			return nil, raw, errLineTooLong
		}
		return line, raw, e
	}
}

// decodeRow decodes a line that must be one JSON object in valid UTF-8; when
// it is not, m is nil and kind / reason say why (never with the content).
func decodeRow(line []byte) (m map[string]any, kind, reason string) {
	if !utf8.Valid(line) {
		return nil, "not_utf8", "not valid UTF-8"
	}
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	if dec.Decode(&m) != nil || m == nil {
		return nil, "not_json", "not a JSON object"
	}
	var more json.RawMessage
	if dec.Decode(&more) != io.EOF {
		return nil, "not_json", "more than one JSON value on the line"
	}
	return m, "", ""
}

// exceedsDepth reports whether v, found at depth d, nests deeper than limit.
func exceedsDepth(v any, d, limit int) bool {
	switch x := v.(type) {
	case map[string]any:
		if d > limit {
			return true
		}
		for _, e := range x {
			if exceedsDepth(e, d+1, limit) {
				return true
			}
		}
	case []any:
		if d > limit {
			return true
		}
		for _, e := range x {
			if exceedsDepth(e, d+1, limit) {
				return true
			}
		}
	}
	return false
}

// scrubRow is the fixture form of one row, or nil and the reason it goes.
func scrubRow(m map[string]any, rw *rewriter) (map[string]any, string) {
	typ, _ := m["type"].(string)
	sub, _ := m["subtype"].(string)
	att := ""
	if a, ok := m["attachment"].(map[string]any); ok {
		att, _ = a["type"].(string)
	}
	if !ccnorm.ReadsRow(typ, sub, att) {
		switch typ {
		case "system":
			return nil, "system:" + sub
		case "attachment":
			return nil, "attachment:" + att
		}
		return nil, "type:" + typ
	}
	out := keepMap(m, "", indexFor(typ), rw)
	// identity metadata: not read by the normalizer, but the fixture rows
	// stay realistic and the case's cc_version comes from `version`
	for _, k := range []string{"cwd", "sessionId", "session_id", "gitBranch", "version"} {
		s, ok := m[k].(string)
		if !ok {
			continue
		}
		switch k {
		case "cwd":
			s = FixtureCwd
		case "sessionId", "session_id":
			s = FixtureSessionID
		case "gitBranch":
			s = FixtureBranch
		}
		out[k] = s
	}
	return out, ""
}

// ---- field selection ------------------------------------------------------

// index is ReadFields as the scrubber walks it, for one row type.
type index struct {
	exact, subtree, prefix map[string]bool
}

var indexes = map[string]*index{}

func indexFor(rowType string) *index {
	if ix, ok := indexes[rowType]; ok {
		return ix
	}
	ix := &index{map[string]bool{}, map[string]bool{}, map[string]bool{}}
	for _, f := range ccnorm.ReadFields {
		if f.Row != rowType && f.Row != "*" {
			continue
		}
		ix.exact[f.Path] = true
		if f.Subtree {
			ix.subtree[f.Path] = true
		}
		// every proper prefix is a place to descend through
		cur := ""
		segs := strings.Split(f.Path, ".")
		for i, seg := range segs {
			base, isArr := strings.CutSuffix(seg, "[]")
			if cur != "" {
				cur += "."
			}
			cur += base
			if isArr {
				ix.prefix[cur] = true
				cur += "[]"
			}
			if i < len(segs)-1 {
				ix.prefix[cur] = true
			}
		}
	}
	indexes[rowType] = ix
	return ix
}

func (ix *index) wants(p string) bool { return ix.exact[p] || ix.prefix[p] || ix.subtree[p] }

func join(path, k string) string {
	if path == "" {
		return k
	}
	return path + "." + k
}

// keepMap keeps the members of an object that the index wants.
func keepMap(m map[string]any, path string, ix *index, rw *rewriter) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		if p := join(path, k); ix.wants(p) {
			if kept, ok := keepValue(v, p, ix, rw, k); ok {
				out[k] = kept
			}
		}
	}
	return out
}

// keepValue keeps v found at path. A subtree is kept whole; an object or
// array is entered (and kept, possibly empty, so a member's presence and a
// block count do not change); a scalar is kept only at an exact entry.
func keepValue(v any, path string, ix *index, rw *rewriter, key string) (any, bool) {
	if ix.subtree[path] {
		return rw.deep(v, key), true
	}
	switch x := v.(type) {
	case map[string]any:
		return keepMap(x, path, ix, rw), true
	case []any:
		out := []any{}
		ep := path + "[]"
		if !ix.wants(ep) {
			return out, true
		}
		for _, e := range x {
			if kept, ok := keepValue(e, ep, ix, rw, ""); ok {
				out = append(out, kept)
			}
		}
		return out, true
	}
	if !ix.exact[path] {
		return nil, false
	}
	if strings.HasSuffix(path, ".source.data") {
		return TinyPNG, true
	}
	return rw.deep(v, key), true
}

// ---- string rewriting -----------------------------------------------------

var (
	// the recording hosts: a pdx address <host>/<name> becomes host/fixture-peer,
	// a bare host name becomes the word host
	rePdxAddr  = regexp.MustCompile(`(?i)\b(?:mlab|air26|air19|air-2026|air-2019)/[A-Za-z0-9_-]+`)
	reHostName = regexp.MustCompile(`(?i)\b(?:mlab|air26|air19|air-2026|air-2019)\b`)
	reUDS      = regexp.MustCompile("uds:[^\\s\"'`<>\\\\]*/[0-9]+\\.sock")
	reProjects = regexp.MustCompile(`\.claude/projects/[A-Za-z0-9._-]+(?:/[A-Za-z0-9._/-]*)?`)
	reUUID     = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	reMcp      = regexp.MustCompile(`mcp__[A-Za-z0-9_*-]+`)
	// a /context table row: | mcp__server__tool | <server> | <tokens> |
	reMcpRow    = regexp.MustCompile(`(mcp__server__tool \| )[A-Za-z0-9_-]+( \|)`)
	reEmail     = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}`)
	reClaudeTmp = regexp.MustCompile("(?:/private)?/tmp/claude-[0-9]+/[^/\\s\"'`<>\\\\]+")
	reVarTmp    = regexp.MustCompile("(?:/private)?/var/folders/[^/\\s\"'`<>\\\\]+/[^/\\s\"'`<>\\\\]+/[A-Za-z0-9]")
	reTmp       = regexp.MustCompile(`(^|[^A-Za-z0-9_.\-/])(?:/private)?/tmp($|[^A-Za-z0-9_])`)
	reHome      = regexp.MustCompile("/(?:Users|home)/[^/\\s\"'`<>\\\\]+")
	reEncHome   = regexp.MustCompile(`-Users-[A-Za-z0-9._]+`)
	reTailnet   = regexp.MustCompile(`100\.64\.\d{1,3}\.\d{1,3}`)
	// private (RFC 1918) addresses
	rePrivateIP = regexp.MustCompile(`(?:^|[^\d.])(?:10\.\d{1,3}|172\.(?:1[6-9]|2\d|3[01])|192\.168)\.\d{1,3}\.\d{1,3}`)
	// credential prefixes, in any letter case
	reBearer = regexp.MustCompile("(?i)\\bBearer(?:\\s+[^\\s\"'`]*)?")
	reSK     = regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])sk-[A-Za-z0-9_\-]{8,}`)
	reGH     = regexp.MustCompile(`(?i)gh[pousr]_[A-Za-z0-9_]*`)
	reSlack  = regexp.MustCompile(`(?i)xox[a-z]-[A-Za-z0-9\-]*`)
	reAWS    = regexp.MustCompile(`(?i)AKIA[0-9A-Z]{16}`)

	// a run of the base64 alphabets (standard and URL-safe, with padding):
	// a candidate for LooksLikeToken
	reLongRun = regexp.MustCompile(`[A-Za-z0-9+/_-]{32,}={0,2}`)
	// ids that are kept as they are inside a run: a message / tool / request
	// id, a uuid, a subagent id (a + 16 hex characters)
	reKeptID = regexp.MustCompile(`\b(?:toolu|msg|req)_[A-Za-z0-9]+|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|\ba[0-9a-f]{16}\b`)
)

// LooksLikeToken reports whether s, a stretch of the base64 alphabets, is
// secret-shaped: 32 or more characters mixing letters and digits, not a
// canonical uuid, and (so that paths and hyphenated words are left alone)
// holding at least one piece, between "/", "-" and "_", of 8 or more
// characters that itself mixes letters and digits. What the scrubber redacts
// and the fixture guard refuses.
func LooksLikeToken(s string) bool {
	if len(s) < 32 || reUUIDExact.MatchString(s) {
		return false
	}
	var letter, digit bool
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
			digit = true
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			letter = true
		}
	}
	if !letter || !digit {
		return false
	}
	for _, piece := range strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '-' || r == '_' }) {
		if len(piece) < 8 {
			continue
		}
		var l, d bool
		for _, c := range piece {
			switch {
			case c >= '0' && c <= '9':
				d = true
			case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
				l = true
			}
		}
		if l && d {
			return true
		}
	}
	return false
}

var reUUIDExact = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// TokenSpans finds the secret-shaped stretches of s as [from, to) offsets:
// inside every run of the base64 alphabets, what lies between the ids that
// are kept (uuid, toolu_/msg_/req_ ids, subagent ids) and passes
// LooksLikeToken. The scrubber redacts these and the fixture guard refuses them.
func TokenSpans(s string) [][2]int {
	var out [][2]int
	for _, run := range reLongRun.FindAllStringIndex(s, -1) {
		m := s[run[0]:run[1]]
		check := func(from, to int) {
			if LooksLikeToken(m[from:to]) {
				out = append(out, [2]int{run[0] + from, run[0] + to})
			}
		}
		last := 0
		for _, id := range reKeptID.FindAllStringIndex(m, -1) {
			// a kept id only counts as one when no letter or digit touches it:
			// a uuid inside a longer secret-shaped string is part of the secret
			if id[0] > 0 && isAlnum(m[id[0]-1]) || id[1] < len(m) && isAlnum(m[id[1]]) {
				continue
			}
			check(last, id[0])
			last = id[1]
		}
		check(last, len(m))
	}
	return out
}

func isAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func redactTokens(s string) string {
	spans := TokenSpans(s)
	if len(spans) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, sp := range spans {
		b.WriteString(s[last:sp[0]])
		b.WriteString("[redacted-token]")
		last = sp[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

type rewriter struct {
	exact   *strings.Replacer // recorded cwd / session ids, longest first
	userRes []*regexp.Regexp
	home    *regexp.Regexp
}

func newRewriter(o Options, rows []map[string]any) *rewriter {
	home := o.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	users := o.Users
	if users == nil {
		if u, err := user.Current(); err == nil && u.Username != "" {
			users = append(users, u.Username)
		}
		if home != "" {
			users = append(users, filepath.Base(home))
		}
	}
	rw := &rewriter{}
	for _, u := range users {
		if u != "" && u != "user" {
			rw.userRes = append(rw.userRes, regexp.MustCompile(`\b`+regexp.QuoteMeta(u)+`\b`))
		}
	}
	if home != "" && home != "/" {
		rw.home = regexp.MustCompile(regexp.QuoteMeta(strings.TrimRight(home, "/")) + `(?:/|\b)`)
	}
	// the identity the rows record, replaced wherever it shows up in text
	seen := map[string]string{}
	for _, m := range rows {
		for _, k := range []string{"sessionId", "session_id"} {
			if s, ok := m[k].(string); ok && s != "" && s != FixtureSessionID {
				seen[s] = FixtureSessionID
			}
		}
		if s, ok := m["cwd"].(string); ok && len(s) > 1 && s != FixtureCwd {
			seen[s] = FixtureCwd
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return len(keys[i]) > len(keys[j]) || len(keys[i]) == len(keys[j]) && keys[i] < keys[j]
	})
	var pairs []string
	for _, k := range keys {
		pairs = append(pairs, k, seen[k])
	}
	rw.exact = strings.NewReplacer(pairs...)
	return rw
}

// idKeys are the keys whose values are ids. A value under such a key that has
// the shape of a structural id (structuralID) is kept as recorded; any other
// string under it is ordinary text and gets every redaction rule: a tool input
// is free-form and may well have an "id" or "uuid" member holding a secret.
var idKeys = map[string]bool{"uuid": true, "tool_use_id": true, "id": true, "agentId": true, "timestamp": true}

var reStructuralID = regexp.MustCompile(`^(?:(?:toolu|msg|req)_[A-Za-z0-9]+|a[0-9a-f]{16}|\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z)$`)

// hasCredential reports whether s holds a credential-prefixed value, which a
// structural-looking id must not smuggle past the redaction rules
// (toolu_AKIA...).
func hasCredential(s string) bool {
	return reBearer.MatchString(s) || reSK.MatchString(s) || reGH.MatchString(s) || reSlack.MatchString(s) || reAWS.MatchString(s)
}

func structuralID(s string) bool { return reUUIDExact.MatchString(s) || reStructuralID.MatchString(s) }

// deep rewrites every string under v (json.Number and the rest as they are).
func (rw *rewriter) deep(v any, key string) any {
	switch x := v.(type) {
	case string:
		if idKeys[key] && structuralID(x) && !hasCredential(x) {
			return x
		}
		return rw.str(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = rw.deep(e, k)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = rw.deep(e, key)
		}
		return out
	}
	return v
}

// neutralProjectsPath rewrites one .claude/projects/<dir>[/rest] match.
func neutralProjectsPath(m string) string {
	rest := strings.TrimPrefix(m, ".claude/projects/")
	tail := ""
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		tail = reUUID.ReplaceAllString(rest[i:], FixtureSessionID)
	}
	return ".claude/projects/" + FixtureProjectDir + tail
}

// neutralMcpName rewrites one mcp__… name: the server is whatever precedes
// the first "__" after the prefix (a server name has single underscores
// only), the tool whatever follows.
func neutralMcpName(m string) string {
	if i := strings.Index(strings.TrimPrefix(m, "mcp__"), "__"); i > 0 {
		return "mcp__" + FixtureMcpServer + "__" + FixtureMcpTool
	}
	return "mcp__" + FixtureMcpServer
}

// OmittedLocalCommandOutput replaces every local-command stdout longer than
// MaxLocalCommandOutput bytes: a /context listing names the recording host's
// installed plugins, skills and mcp servers. Row structure and ids stay.
const (
	OmittedLocalCommandOutput = "<local-command-stdout>[output omitted by scrubber]</local-command-stdout>"
	MaxLocalCommandOutput     = 1024
)

// omitLocalCommandOutput reports whether s is a whole <local-command-stdout>
// element (ANSI colours and all) past the limit.
func omitLocalCommandOutput(s string) bool {
	if len(s) <= MaxLocalCommandOutput {
		return false
	}
	t := strings.TrimSpace(s)
	return strings.HasPrefix(t, "<local-command-stdout>") && strings.HasSuffix(t, "</local-command-stdout>")
}

// OmittedContextUsage replaces a /context expansion (the isMeta user row's
// markdown dump) past MaxLocalCommandOutput bytes: it lists the same host
// plugins and skills as the local-command form.
const OmittedContextUsage = "## Context Usage\n[output omitted by scrubber]"

func omitContextUsage(s string) bool {
	return len(s) > MaxLocalCommandOutput && strings.HasPrefix(s, "## Context Usage")
}

func (rw *rewriter) str(s string) string {
	if omitLocalCommandOutput(s) {
		return OmittedLocalCommandOutput
	}
	if omitContextUsage(s) {
		return OmittedContextUsage
	}
	s = rw.exact.Replace(s)
	s = reUDS.ReplaceAllString(s, FixtureSocket)
	s = rePdxAddr.ReplaceAllString(s, FixtureHost+"/"+FixturePeer)
	s = reHostName.ReplaceAllString(s, FixtureHost)
	s = reEmail.ReplaceAllString(s, "user@example.com")
	s = reProjects.ReplaceAllStringFunc(s, neutralProjectsPath)
	s = reMcp.ReplaceAllStringFunc(s, neutralMcpName)
	s = reMcpRow.ReplaceAllString(s, "${1}"+FixtureMcpServer+"${2}")
	s = reClaudeTmp.ReplaceAllString(s, "/work/tmp")
	s = reVarTmp.ReplaceAllString(s, "/work/tmp")
	for i := 0; i < 3; i++ { // adjacent matches share their delimiter
		t := reTmp.ReplaceAllString(s, "${1}/work/tmp${2}")
		if t == s {
			break
		}
		s = t
	}
	if rw.home != nil {
		s = rw.home.ReplaceAllStringFunc(s, func(m string) string {
			if strings.HasSuffix(m, "/") {
				return "/work/"
			}
			return "/work"
		})
	}
	s = reHome.ReplaceAllString(s, "/work")
	s = reEncHome.ReplaceAllString(s, "-work")
	for _, re := range rw.userRes {
		s = re.ReplaceAllString(s, "user")
	}
	s = reTailnet.ReplaceAllString(s, "192.0.2.1")
	s = rePrivateIP.ReplaceAllStringFunc(s, func(m string) string {
		// the pattern may have taken one character before the address
		return m[:strings.IndexAny(m, "0123456789")] + "192.0.2.1"
	})
	s = reBearer.ReplaceAllString(s, "[redacted-auth]")
	s = reSK.ReplaceAllString(s, "${1}[redacted-key]")
	s = reGH.ReplaceAllString(s, "[redacted-token]")
	s = reSlack.ReplaceAllString(s, "[redacted-token]")
	s = reAWS.ReplaceAllString(s, "[redacted-key]")
	return redactTokens(s)
}
