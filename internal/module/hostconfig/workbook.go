package hostconfig

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/workbooksettings"
)

// KeyWorkbook is the host_config row of the session workbook's setting (spec §7: push_wait_s). The workbook and push
// modules read it through workbooksettings.Key.
const KeyWorkbook = "workbook"

// workbookDefaultJSON is workbooksettings.Default() as the GET answers it for a never-written key (emptyFor).
var workbookDefaultJSON = func() string {
	b, err := json.Marshal(workbooksettings.Default())
	if err != nil {
		panic(err) // a fixed struct of one int
	}
	return string(b)
}()

// normalizeWorkbook validates a PUT body (and a stored value): an object of exactly {"push_wait_s": integer 0–30}. A
// misspelt or missing field is refused, so a save never means "left out = default".
func normalizeWorkbook(raw json.RawMessage) (workbooksettings.Settings, error) {
	var fields map[string]json.RawMessage
	if firstByte(raw) != '{' || json.Unmarshal(raw, &fields) != nil {
		return workbooksettings.Settings{}, errors.New("items must be a JSON object")
	}
	for k := range fields {
		if k != "push_wait_s" {
			return workbooksettings.Settings{}, errors.New("unknown workbook field " + k + "; only push_wait_s")
		}
	}
	v, present := fields["push_wait_s"]
	var n int
	if !present || isJSONNull(v) || json.Unmarshal(v, &n) != nil {
		return workbooksettings.Settings{}, errors.New("push_wait_s must be an integer")
	}
	if n < 0 || n > workbooksettings.MaxPushWaitS {
		return workbooksettings.Settings{}, fmt.Errorf("push_wait_s must be between 0 and %d", workbooksettings.MaxPushWaitS)
	}
	return workbooksettings.Settings{PushWaitS: n}, nil
}

// readWorkbook is normalizeWorkbook's lenient twin (the GET's view): a value that does not read is invalid and answers
// the default items (never what its owner did not write as a setting: WorkbookSettings refuses it).
func readWorkbook(raw json.RawMessage) readout {
	s, err := normalizeWorkbook(raw)
	if err != nil {
		return readout{items: workbooksettings.Default(), invalid: err}
	}
	return readout{items: s}
}

// WorkbookSettings reads the stored setting, the default for a never-written key. A stored value that no longer reads
// is an error.
func (m *Module) WorkbookSettings() (workbooksettings.Settings, error) {
	e, err := m.store.Get(KeyWorkbook)
	if err != nil {
		return workbooksettings.Settings{}, err
	}
	if e.Value == nil {
		return workbooksettings.Default(), nil
	}
	return normalizeWorkbook(e.Value)
}
