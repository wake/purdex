package hostconfig

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRejectDuplicateKeys(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"flat ok", `{"a":1,"b":2}`, false},
		{"flat dup", `{"a":1,"a":2}`, true},
		{"nested dup inside array element", `{"items":[{"id":"x"},{"id":"y","id":"z"}]}`, true},
		{"nested dup inside object", `{"items":{"cc":{"exact":"a","exact":"b"}}}`, true},
		{"dup map key", `{"items":{"cc":{},"cc":{}}}`, true},
		{"same key in sibling objects ok", `{"items":[{"id":"x","path":"/"},{"id":"y","path":"/"}],"o":{"id":1}}`, false},
		{"same key at different levels ok", `{"id":{"id":{"id":1}}}`, false},
		{"scalars and arrays ok", `[1,"a",true,null,[],{},[{"k":1.5e3}]]`, false},
		{"trailing data", `{"a":1}{"b":2}`, true},
		{"trailing garbage", `{"a":1} x`, true},
		{"trailing whitespace ok", "{\"a\":1} \n", false},
		{"invalid json", `{"a":`, true},
		{"empty", ``, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := rejectDuplicateKeys([]byte(c.raw))
			if c.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
