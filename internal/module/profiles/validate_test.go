package profiles

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateProfileID(t *testing.T) {
	assert.NoError(t, validateProfileID("p_0123456789ab"))
	for _, bad := range []string{
		"",
		"p_0123456789AB",   // uppercase hex
		"p_0123456789a",    // too short
		"p_0123456789abc",  // too long
		"0123456789abcd",   // missing prefix
		"c_0123456789ab",   // a clientId is not a profileId
		"p_0123456789ag",   // non-hex
		" p_0123456789ab",  // leading space
		"p_0123456789ab\n", // trailing newline
	} {
		assert.Error(t, validateProfileID(bad), "profileId %q", bad)
	}
}

func TestValidateClientID(t *testing.T) {
	assert.NoError(t, validateClientID("c_0123456789ab"))
	for _, bad := range []string{
		"",
		"c_0123456789AB",   // uppercase hex
		"c_0123456789a",    // too short
		"c_0123456789abc",  // too long
		"0123456789abcd",   // missing prefix
		"p_0123456789ab",   // a profileId is not a clientId
		"c_0123456789ag",   // non-hex
		"c_0123456789ab\n", // trailing newline
	} {
		assert.Error(t, validateClientID(bad), "clientId %q", bad)
	}
}

// nameValidators runs the same table against both display-name validators:
// the rule is identical, only the field named in the error differs.
var nameValidators = map[string]func(string) (string, error){
	"name":       validateName,
	"deviceName": validateDeviceName,
}

func TestValidateNames(t *testing.T) {
	ok64 := strings.Repeat("機", 64) // 64 runes, 192 bytes: runes are counted, not bytes

	for field, validate := range nameValidators {
		t.Run(field, func(t *testing.T) {
			for _, tc := range []struct{ in, want string }{
				{"Work", "Work"},
				{"  MacBook  ", "MacBook"},
				{"\tMacBook\n", "MacBook"}, // outer whitespace is trimmed, not rejected
				{"a", "a"},
				{"我的 工作區", "我的 工作區"}, // inner space is printable
				{"dev 🚀", "dev 🚀"},
				{ok64, ok64},
				{" " + ok64 + " ", ok64}, // length is measured after the trim
			} {
				got, err := validate(tc.in)
				require.NoError(t, err, "%s %q", field, tc.in)
				assert.Equal(t, tc.want, got)
			}

			for _, bad := range []string{
				"",
				"   ",
				"\t\n",
				strings.Repeat("機", 65),
				strings.Repeat("a", 65),
				"a\x00b",     // NUL
				"a\tb",       // inner tab is a control character
				"a\nb",       // inner newline
				"a\x1b[31mb", // ESC
				"a\x7fb",     // DEL
				"a\u0085b",   // C1 control (NEL)
				"a\u200bb",   // zero-width space: not printable
				"a\u202eb",   // RTL override: not printable
				"a\xffb",     // invalid UTF-8
			} {
				got, err := validate(bad)
				assert.Error(t, err, "%s %q", field, bad)
				assert.Empty(t, got)
			}
		})
	}
}

func TestValidateNameErrorsNameTheirField(t *testing.T) {
	_, err := validateName("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name")
	assert.NotContains(t, err.Error(), "deviceName")

	_, err = validateDeviceName("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deviceName")
}

func TestValidateSection(t *testing.T) {
	for _, ok := range []string{
		"hosts",
		"settings",
		"workspaces",
		"tabs.a",
		"tabs.ws_01-AZ",
		"tabs." + strings.Repeat("a", 64),
	} {
		assert.NoError(t, validateSection(ok), "section %q", ok)
	}
	for _, bad := range []string{
		"",
		"tabs",
		"tabs.", // empty id
		"tabs.a.b",
		"tabs." + strings.Repeat("a", 65),
		"tabs.a b",
		"tabs.a/b",
		"tabs.機",
		"Hosts",
		"TABS.a",
		"hosts.a",
		"hostsx",
		"xhosts",
		" hosts",
		"hosts\n",
		"tabs.a\n",
		"layout",
	} {
		assert.Error(t, validateSection(bad), "section %q", bad)
	}
}

// hashValidators: hash and fingerprint share one rule (64 lowercase hex).
var hashValidators = map[string]func(string) error{
	"hash":        validateHash,
	"fingerprint": validateFingerprint,
}

func TestValidateHashes(t *testing.T) {
	for field, validate := range hashValidators {
		t.Run(field, func(t *testing.T) {
			assert.NoError(t, validate(strings.Repeat("0", 64)))
			assert.NoError(t, validate(strings.Repeat("0123456789abcdef", 4)))

			for _, bad := range []string{
				"",
				strings.Repeat("a", 63),
				strings.Repeat("a", 65),
				strings.Repeat("A", 64),        // uppercase hex
				strings.Repeat("a", 63) + "g",  // non-hex
				strings.Repeat("a", 64) + "\n", // trailing newline
				"0x" + strings.Repeat("a", 62), // prefixed
				" " + strings.Repeat("a", 63),  // leading space
				"sha256:" + strings.Repeat("a", 57),
			} {
				err := validate(bad)
				require.Error(t, err, "%s %q", field, bad)
				assert.Contains(t, err.Error(), field)
			}
		})
	}
}

func TestValidateOrdinal(t *testing.T) {
	assert.NoError(t, validateOrdinal(1))
	assert.NoError(t, validateOrdinal(2))
	assert.NoError(t, validateOrdinal(1<<40))
	assert.Error(t, validateOrdinal(0))
	assert.Error(t, validateOrdinal(-1))
}

func TestValidateBaseRev(t *testing.T) {
	assert.NoError(t, validateBaseRev(0))
	assert.NoError(t, validateBaseRev(1))
	assert.NoError(t, validateBaseRev(1<<40))
	assert.Error(t, validateBaseRev(-1))
}

func TestValidatePayloadShape(t *testing.T) {
	for _, ok := range []string{
		`{}`,
		`{"a":1}`,
		`{"nested":{"list":[1,2,3],"s":"x","n":null}}`,
		" \t\r\n{\"a\":1}\n", // surrounding whitespace is still one object
	} {
		assert.NoError(t, validatePayload(json.RawMessage(ok)), "payload %q", ok)
	}

	for _, bad := range []string{
		``,
		`   `,
		`null`,
		`[]`,
		`[{}]`,
		`"x"`,
		`"{}"`,
		`1`,
		`true`,
		`{`,
		`{"a":}`,
		`{"a":1,}`,
		`{a:1}`,
		`{} {}`,     // two values
		`{}garbage`, // trailing junk
	} {
		err := validatePayload(json.RawMessage(bad))
		require.Error(t, err, "payload %q", bad)
		assert.False(t, errors.Is(err, ErrPayloadTooLarge), "payload %q is malformed, not oversized", bad)
	}

	assert.Error(t, validatePayload(nil))
}

// objectOfSize builds a valid JSON object that is exactly n bytes long.
func objectOfSize(t *testing.T, n int) json.RawMessage {
	t.Helper()
	const wrapper = `{"p":""}`
	raw := json.RawMessage(`{"p":"` + strings.Repeat("a", n-len(wrapper)) + `"}`)
	require.Len(t, raw, n)
	return raw
}

func TestValidatePayloadSize(t *testing.T) {
	assert.Equal(t, 5*1024*1024, PayloadCap)

	assert.NoError(t, validatePayload(objectOfSize(t, PayloadCap-1)))
	assert.NoError(t, validatePayload(objectOfSize(t, PayloadCap)), "exactly 5 MiB is allowed")

	err := validatePayload(objectOfSize(t, PayloadCap+1))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPayloadTooLarge), "5 MiB + 1 must be the sentinel, got %v", err)

	// Size wins over shape: an oversized body is never parsed, so the handler
	// answers 413 no matter what the bytes are.
	err = validatePayload(json.RawMessage(strings.Repeat("[", PayloadCap+1)))
	assert.True(t, errors.Is(err, ErrPayloadTooLarge))

	// Whitespace counts: the cap is on the bytes as sent.
	padded := append(json.RawMessage(strings.Repeat(" ", PayloadCap-1)), []byte(`{}`)...)
	assert.True(t, errors.Is(validatePayload(padded), ErrPayloadTooLarge))
}
