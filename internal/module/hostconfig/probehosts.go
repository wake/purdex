package hostconfig

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/wake/purdex/internal/fleet"
)

// KeyProbeHosts is the host_config row of the hosts the fleet only probes over SSH (fleet spec §6, §8.2).
const KeyProbeHosts = "probe_hosts"

// ProbeHostsKey is the registry key of the module as a ProbeHostsReader (the fleet module reads the rows through it).
const ProbeHostsKey = "hostconfig.probe-hosts"

const (
	maxProbeHosts     = 16
	probeNameMaxRunes = 40
)

var (
	probeIDPattern    = regexp.MustCompile(`^[0-9a-f]{8}$`)
	hexColorPattern   = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	probeIconWeights  = []string{"bold", "regular", "thin", "light", "fill", "duotone"}
	probeColorModes   = []string{"console", "terminal", "execution"}
	probeColorLayers  = []string{"main", "middle", "light"}
	errProbeColorsObj = errors.New("colors must be an object")
)

// sshAliasOK says whether alias is a Host in the daemon user's SSH config (fleet.ValidAlias on the real home). A variable
// so that the tests stand for the SSH config; the readers are package functions with no module to ask for a home.
var sshAliasOK = func(alias string) bool {
	home, err := os.UserHomeDir()
	return err == nil && fleet.ValidAlias(home, alias)
}

// ProbeHost is one host the fleet only looks at: no daemon runs there. Colors has the App's HostConfig.colors shape
// (spa/src/lib/host-color.ts) and is kept as the JSON the rules above accepted.
type ProbeHost struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	SSHAlias   string          `json:"ssh_alias"`
	Icon       string          `json:"icon"`
	IconWeight string          `json:"icon_weight"`
	Colors     json.RawMessage `json:"colors,omitempty"`
}

// ProbeHostsReader is what the fleet module type-asserts on the registry value.
type ProbeHostsReader interface {
	ProbeHosts() ([]ProbeHost, int64, error)
}

// probeRowIn is a row as it arrives; the raw colors are checked by normalizeProbeColors.
type probeRowIn struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	SSHAlias   string          `json:"ssh_alias"`
	Icon       string          `json:"icon"`
	IconWeight string          `json:"icon_weight"`
	Colors     json.RawMessage `json:"colors"`
}

type probeLayerIn struct {
	Color *string `json:"color"`
	Alpha *int    `json:"alpha"`
}

// strictDecode decodes raw into v refusing unknown keys and trailing data.
func strictDecode(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data")
	}
	return nil
}

// normalizeProbeColors checks colors against the App's HostConfig.colors shape: modes console / terminal / execution, each
// with a main layer (color and alpha) and optional middle / light layers (alpha, color optional); alpha an integer 0 to 100,
// color #rrggbb; nothing else. Absent or null is no colors. The result is the canonical JSON (empty for none).
func normalizeProbeColors(raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return nil, nil
	}
	if firstByte(raw) != '{' {
		return nil, errProbeColorsObj
	}
	var modes map[string]json.RawMessage
	if err := json.Unmarshal(raw, &modes); err != nil {
		return nil, errProbeColorsObj
	}
	out := map[string]map[string]probeLayerOut{}
	for mode, setRaw := range modes {
		if !slices.Contains(probeColorModes, mode) {
			return nil, fmt.Errorf("colors: unknown mode %q", mode)
		}
		if firstByte(setRaw) != '{' {
			return nil, fmt.Errorf("colors.%s must be an object", mode)
		}
		var layers map[string]json.RawMessage
		if err := json.Unmarshal(setRaw, &layers); err != nil {
			return nil, fmt.Errorf("colors.%s must be an object", mode)
		}
		set := map[string]probeLayerOut{}
		for layer, layerRaw := range layers {
			if !slices.Contains(probeColorLayers, layer) {
				return nil, fmt.Errorf("colors.%s: unknown layer %q", mode, layer)
			}
			var l probeLayerIn
			if firstByte(layerRaw) != '{' || strictDecode(layerRaw, &l) != nil {
				return nil, fmt.Errorf("colors.%s.%s must be {color?, alpha}", mode, layer)
			}
			if l.Alpha == nil || *l.Alpha < 0 || *l.Alpha > 100 {
				return nil, fmt.Errorf("colors.%s.%s: alpha must be an integer 0 to 100", mode, layer)
			}
			if l.Color != nil && !hexColorPattern.MatchString(*l.Color) {
				return nil, fmt.Errorf("colors.%s.%s: color must be #rrggbb", mode, layer)
			}
			if layer == "main" && l.Color == nil {
				return nil, fmt.Errorf("colors.%s.main: color is required", mode)
			}
			set[layer] = probeLayerOut{Color: l.Color, Alpha: *l.Alpha}
		}
		if _, ok := set["main"]; !ok {
			return nil, fmt.Errorf("colors.%s: main is required", mode)
		}
		out[mode] = set
	}
	if len(out) == 0 {
		return nil, nil
	}
	return json.Marshal(out)
}

type probeLayerOut struct {
	Color *string `json:"color,omitempty"`
	Alpha int     `json:"alpha"`
}

// checkProbeHost applies the PUT's rules to one row, in their order, and answers it as stored. kept holds the ids of the rows
// kept so far (a row takes its id only once the whole row passes). An empty id is generated (8 hex, avoiding kept and
// reserved, the ids later rows bring) when generate is set, else the row is refused: the lenient read must give the same id
// every time. A given id must be 8 hex (the App writes one row to two daemons and sends the id one of them made).
func checkProbeHost(in json.RawMessage, kept, reserved map[string]bool, generate bool) (ProbeHost, error) {
	var r probeRowIn
	if err := strictDecode(in, &r); err != nil {
		return ProbeHost{}, fmt.Errorf("invalid probe host: %v", err)
	}
	id := r.ID
	if id != "" {
		if !probeIDPattern.MatchString(id) {
			return ProbeHost{}, fmt.Errorf("invalid id %q", id)
		}
		if kept[id] {
			return ProbeHost{}, fmt.Errorf("duplicate id %q", id)
		}
	} else if !generate {
		return ProbeHost{}, errors.New("id is required")
	}
	name := strings.TrimSpace(r.Name)
	if n := utf8.RuneCountInString(name); n < 1 || n > probeNameMaxRunes || hasControl(name, false) {
		return ProbeHost{}, fmt.Errorf("name must be 1 to %d characters", probeNameMaxRunes)
	}
	if !sshAliasOK(r.SSHAlias) {
		return ProbeHost{}, fmt.Errorf("unknown_ssh_alias: %s", r.SSHAlias)
	}
	if r.Icon != "" && !phosphorPattern.MatchString(r.Icon) {
		return ProbeHost{}, fmt.Errorf("invalid phosphor icon %q", r.Icon)
	}
	if r.IconWeight != "" && !slices.Contains(probeIconWeights, r.IconWeight) {
		return ProbeHost{}, fmt.Errorf("invalid icon_weight %q", r.IconWeight)
	}
	colors, err := normalizeProbeColors(r.Colors)
	if err != nil {
		return ProbeHost{}, err
	}
	for id == "" {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return ProbeHost{}, fmt.Errorf("generate id: %v", err)
		}
		if c := hex.EncodeToString(b[:]); !kept[c] && !reserved[c] {
			id = c
		}
	}
	kept[id] = true
	return ProbeHost{ID: id, Name: name, SSHAlias: r.SSHAlias, Icon: r.Icon, IconWeight: r.IconWeight, Colors: colors}, nil
}

// normalizeProbeHosts validates a PUT body: an array of at most 16 rows, all passing. A row keeps the id it sends and gets a
// generated one when it sends none.
func normalizeProbeHosts(raw json.RawMessage, _ Entry) ([]ProbeHost, error) {
	var in []json.RawMessage
	if err := decodeArray(raw, &in); err != nil {
		return nil, err
	}
	if len(in) > maxProbeHosts {
		return nil, fmt.Errorf("at most %d probe hosts", maxProbeHosts)
	}
	reserved := map[string]bool{}
	for _, e := range in {
		var peek struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(e, &peek) == nil && peek.ID != "" {
			reserved[peek.ID] = true
		}
	}
	out := make([]ProbeHost, 0, len(in))
	kept := map[string]bool{}
	for i, e := range in {
		row, err := checkProbeHost(e, kept, reserved, true)
		if err != nil {
			return nil, fmt.Errorf("probe host %d: %v", i, err)
		}
		out = append(out, row)
	}
	return out, nil
}

// readProbeHosts is normalizeProbeHosts' lenient twin: a row that fails is left out with its reason.
func readProbeHosts(raw json.RawMessage) readout {
	kept := map[string]bool{}
	return readRows(raw, "probe host", "probe hosts", maxProbeHosts, func(e json.RawMessage) (ProbeHost, error) {
		return checkProbeHost(e, kept, nil, false)
	})
}

// ProbeHosts reads the stored rows as the GET reads them (a row that no longer validates, a gone alias included, is left
// out), with the row's revision (0 when never written). A stored value that is not a list is an error.
func (m *Module) ProbeHosts() ([]ProbeHost, int64, error) {
	e, err := m.store.Get(KeyProbeHosts)
	if err != nil {
		return nil, 0, err
	}
	if e.Value == nil {
		return []ProbeHost{}, e.Revision, nil
	}
	r := readProbeHosts(e.Value)
	if r.invalid != nil {
		return nil, e.Revision, r.invalid
	}
	return r.items.([]ProbeHost), e.Revision, nil
}

var _ ProbeHostsReader = (*Module)(nil)
