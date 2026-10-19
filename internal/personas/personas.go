// Package personas is the narrow read of the host setting `personas` (name pool v2 spec §4), for modules that cannot import
// hostconfig: the peers module builds its name pool from it, and the journal shows a persona's display name.
package personas

import "encoding/json"

// Key is the service-registry key under which the hostconfig module publishes itself as a Reader.
const Key = "hostconfig.personas"

// MaxPersonas bounds a collection, retired ones included.
const MaxPersonas = 64

// Persona is one character of the pool (spec §4.1). The daemon checks the code, the sizes and the image; the recipe in
// Avatar is opaque to it.
type Persona struct {
	Code        string          `json:"code"`
	Name        string          `json:"name"`
	Sex         string          `json:"sex"`
	Lead        bool            `json:"lead"`
	Retired     bool            `json:"retired"`
	Nationality Nationality     `json:"nationality"`
	Template    Template        `json:"template"`
	Personality Bilingual       `json:"personality"`
	Method      Bilingual       `json:"method"`
	Interests   Bilingual       `json:"interests"`
	Avatar      json.RawMessage `json:"avatar"`
	AvatarData  string          `json:"avatar_data"`
	UpdatedAt   int64           `json:"updated_at"`
}

type Nationality struct {
	Code string `json:"code"`
	ZH   string `json:"zh"`
	EN   string `json:"en"`
}

type Template struct {
	Name    string `json:"name"`
	NameZH  string `json:"name_zh"`
	FieldZH string `json:"field_zh"`
	FieldEN string `json:"field_en"`
}

type Bilingual struct {
	ZH string `json:"zh"`
	EN string `json:"en"`
}

// Stamp says who wrote the collection last and when (spec §4.3): the later `at` wins, `host` (the larger) on a tie.
type Stamp struct {
	At   int64  `json:"at"`
	Host string `json:"host"`
}

// Set is the stored value: the whole collection and its stamp.
type Set struct {
	Items []Persona `json:"items"`
	Stamp Stamp     `json:"stamp"`
}

// Reader is what a module type-asserts on the registry value. A stored value that no longer reads is an error, not a
// silent empty set: the caller decides what to do without personas.
type Reader interface {
	// Personas is the stored set; a never-written key answers an empty set.
	Personas() (Set, error)
	// OnPersonasChange calls fn after the set changed (a PUT, or a sync from another host); the returned function stops it.
	OnPersonasChange(fn func()) (cancel func())
}
