// Package profilehash is the daemon's port of the SPA's canonical section hash (spa/src/lib/profile/hash.ts): SHA-256 of the
// payload's canonical JSON — object keys sorted at every depth by UTF-16 code unit (as JavaScript's default sort does), arrays
// in order, strings escaped exactly as JSON.stringify writes them, numbers written as ECMAScript Number#toString, -0 as 0.
//
// The daemon needs it to verify the hash a paired phone announces for the tabs it appends (QR pairing spec §5.2 gate 4): a
// Mac fast-forwards, without pulling, a section whose announced hash equals the one it holds, so a wrong hash would be
// invisible to the Macs. The port reads the JSON text itself (encoding/json would replace a lone surrogate with U+FFFD and
// lose the difference): anything it cannot reproduce exactly — a lone surrogate, invalid UTF-8, a number outside float64,
// nesting beyond maxDepth — is an error, never a hash. Shared fixtures, pinned on both sides:
// spa/src/lib/profile/__fixtures__/canonical-hash.json.
package profilehash

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// maxDepth bounds nesting so a hostile payload cannot exhaust the stack (the payload itself is capped at 5 MiB).
const maxDepth = 512

// Sum returns the lowercase-hex SHA-256 of the canonical form of the JSON text raw.
func Sum(raw []byte) (string, error) {
	c, err := Canonical(raw)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(c))
	return hex.EncodeToString(sum[:]), nil
}

// Canonical returns the canonical JSON text of raw (what the SPA's structuralKey returns for the parsed value).
func Canonical(raw []byte) (string, error) {
	if !utf8.Valid(raw) {
		return "", errors.New("profilehash: not valid UTF-8")
	}
	p := &parser{s: raw}
	p.ws()
	v, err := p.value(0)
	if err != nil {
		return "", err
	}
	p.ws()
	if p.i != len(p.s) {
		return "", p.fail("trailing data")
	}
	var b strings.Builder
	write(&b, v)
	return b.String(), nil
}

type kind int

const (
	kNull kind = iota
	kBool
	kNum
	kStr
	kArr
	kObj
)

type node struct {
	k    kind
	b    bool
	num  string // already formatted
	str  string
	arr  []*node
	keys []string // sorted by UTF-16 code unit
	obj  map[string]*node
}

type parser struct {
	s []byte
	i int
}

func (p *parser) fail(msg string) error { return fmt.Errorf("profilehash: %s at byte %d", msg, p.i) }

func (p *parser) ws() {
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

func (p *parser) value(depth int) (*node, error) {
	if depth > maxDepth {
		return nil, p.fail("nesting too deep")
	}
	if p.i >= len(p.s) {
		return nil, p.fail("unexpected end")
	}
	switch c := p.s[p.i]; {
	case c == '{':
		return p.object(depth)
	case c == '[':
		return p.array(depth)
	case c == '"':
		s, err := p.str()
		if err != nil {
			return nil, err
		}
		return &node{k: kStr, str: s}, nil
	case c == '-' || (c >= '0' && c <= '9'):
		return p.number()
	case p.lit("true"):
		return &node{k: kBool, b: true}, nil
	case p.lit("false"):
		return &node{k: kBool}, nil
	case p.lit("null"):
		return &node{k: kNull}, nil
	}
	return nil, p.fail("unexpected character")
}

func (p *parser) lit(w string) bool {
	if len(p.s)-p.i >= len(w) && string(p.s[p.i:p.i+len(w)]) == w {
		p.i += len(w)
		return true
	}
	return false
}

func (p *parser) array(depth int) (*node, error) {
	p.i++ // [
	n := &node{k: kArr}
	p.ws()
	if p.i < len(p.s) && p.s[p.i] == ']' {
		p.i++
		return n, nil
	}
	for {
		p.ws()
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		n.arr = append(n.arr, v)
		p.ws()
		if p.i >= len(p.s) {
			return nil, p.fail("unexpected end")
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case ']':
			p.i++
			return n, nil
		default:
			return nil, p.fail("expected , or ]")
		}
	}
}

func (p *parser) object(depth int) (*node, error) {
	p.i++ // {
	n := &node{k: kObj, obj: map[string]*node{}}
	p.ws()
	if p.i < len(p.s) && p.s[p.i] == '}' {
		p.i++
		return n, nil
	}
	for {
		p.ws()
		if p.i >= len(p.s) || p.s[p.i] != '"' {
			return nil, p.fail("expected a string key")
		}
		k, err := p.str()
		if err != nil {
			return nil, err
		}
		p.ws()
		if p.i >= len(p.s) || p.s[p.i] != ':' {
			return nil, p.fail("expected :")
		}
		p.i++
		p.ws()
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		if _, dup := n.obj[k]; !dup {
			n.keys = append(n.keys, k)
		}
		n.obj[k] = v // a repeated key: the last one wins, as in JSON.parse
		p.ws()
		if p.i >= len(p.s) {
			return nil, p.fail("unexpected end")
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case '}':
			p.i++
			sort.Slice(n.keys, func(a, b int) bool { return lessUTF16(n.keys[a], n.keys[b]) })
			return n, nil
		default:
			return nil, p.fail("expected , or }")
		}
	}
}

// lessUTF16 orders by UTF-16 code unit, as Array.prototype.sort() does for strings (UTF-8 byte order differs for
// supplementary-plane characters against U+E000–U+FFFF).
func lessUTF16(a, b string) bool {
	ua, ub := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

func (p *parser) str() (string, error) {
	p.i++ // opening quote
	var b strings.Builder
	for {
		if p.i >= len(p.s) {
			return "", p.fail("unterminated string")
		}
		c := p.s[p.i]
		switch {
		case c == '"':
			p.i++
			return b.String(), nil
		case c < 0x20:
			return "", p.fail("control character in string")
		case c == '\\':
			p.i++
			if p.i >= len(p.s) {
				return "", p.fail("unterminated escape")
			}
			e := p.s[p.i]
			p.i++
			switch e {
			case '"', '\\', '/':
				b.WriteByte(e)
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'u':
				r, err := p.unicode()
				if err != nil {
					return "", err
				}
				b.WriteRune(r)
			default:
				return "", p.fail("bad escape")
			}
		default:
			r, size := utf8.DecodeRune(p.s[p.i:])
			b.WriteRune(r)
			p.i += size
		}
	}
}

func (p *parser) hex4() (uint16, error) {
	if p.i+4 > len(p.s) {
		return 0, p.fail("short \\u escape")
	}
	v, err := strconv.ParseUint(string(p.s[p.i:p.i+4]), 16, 16)
	if err != nil {
		return 0, p.fail("bad \\u escape")
	}
	p.i += 4
	return uint16(v), nil
}

// unicode reads the XXXX of a \uXXXX (the \u is consumed) and pairs surrogates; a lone one is an error.
func (p *parser) unicode() (rune, error) {
	u, err := p.hex4()
	if err != nil {
		return 0, err
	}
	switch {
	case u >= 0xDC00 && u <= 0xDFFF:
		return 0, p.fail("lone low surrogate")
	case u >= 0xD800 && u <= 0xDBFF:
		if p.i+2 > len(p.s) || p.s[p.i] != '\\' || p.s[p.i+1] != 'u' {
			return 0, p.fail("lone high surrogate")
		}
		p.i += 2
		lo, err := p.hex4()
		if err != nil {
			return 0, err
		}
		if lo < 0xDC00 || lo > 0xDFFF {
			return 0, p.fail("lone high surrogate")
		}
		return utf16.DecodeRune(rune(u), rune(lo)), nil
	}
	return rune(u), nil
}

func (p *parser) number() (*node, error) {
	start := p.i
	if p.s[p.i] == '-' {
		p.i++
	}
	digits := func() int {
		n := 0
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
			n++
		}
		return n
	}
	if p.i >= len(p.s) {
		return nil, p.fail("bad number")
	}
	if p.s[p.i] == '0' {
		p.i++
	} else if digits() == 0 {
		return nil, p.fail("bad number")
	}
	if p.i < len(p.s) && p.s[p.i] == '.' {
		p.i++
		if digits() == 0 {
			return nil, p.fail("bad number")
		}
	}
	if p.i < len(p.s) && (p.s[p.i] == 'e' || p.s[p.i] == 'E') {
		p.i++
		if p.i < len(p.s) && (p.s[p.i] == '+' || p.s[p.i] == '-') {
			p.i++
		}
		if digits() == 0 {
			return nil, p.fail("bad number")
		}
	}
	f, err := strconv.ParseFloat(string(p.s[start:p.i]), 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return nil, p.fail("number outside float64")
	}
	return &node{k: kNum, num: formatNumber(f)}, nil
}

// formatNumber is ECMAScript Number::toString(10) for a finite float64 (-0 → "0").
func formatNumber(f float64) string {
	if f == 0 {
		return "0"
	}
	sign := ""
	if f < 0 {
		sign, f = "-", -f
	}
	// Shortest digits that round-trip: d.ddd e±x
	e := strconv.FormatFloat(f, 'e', -1, 64)
	mant, exps, _ := strings.Cut(e, "e")
	exp, _ := strconv.Atoi(exps)
	digits := strings.Replace(mant, ".", "", 1)
	k, n := len(digits), exp+1
	switch {
	case k <= n && n <= 21:
		return sign + digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return sign + digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return sign + "0." + strings.Repeat("0", -n) + digits
	}
	m := digits[:1]
	if k > 1 {
		m += "." + digits[1:]
	}
	x := n - 1
	es := "+"
	if x < 0 {
		es, x = "-", -x
	}
	return sign + m + "e" + es + strconv.Itoa(x)
}

func write(b *strings.Builder, n *node) {
	switch n.k {
	case kNull:
		b.WriteString("null")
	case kBool:
		if n.b {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case kNum:
		b.WriteString(n.num)
	case kStr:
		quote(b, n.str)
	case kArr:
		b.WriteByte('[')
		for i, v := range n.arr {
			if i > 0 {
				b.WriteByte(',')
			}
			write(b, v)
		}
		b.WriteByte(']')
	case kObj:
		b.WriteByte('{')
		for i, k := range n.keys {
			if i > 0 {
				b.WriteByte(',')
			}
			quote(b, k)
			b.WriteByte(':')
			write(b, n.obj[k])
		}
		b.WriteByte('}')
	}
}

// quote writes s as JSON.stringify does: " and \ escaped, \b \f \n \r \t short, other C0 controls as \u00xx (lowercase hex);
// everything else, DEL and U+2028/2029 included, literally.
func quote(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20:
			fmt.Fprintf(b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}
