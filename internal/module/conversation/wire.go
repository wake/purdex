package conversation

import (
	"encoding/json"
	"strconv"

	"github.com/wake/purdex/internal/convmodel"
)

// indexedItem is a §8.1 item with its API-only `index`: its 0-based position in its turn's FULL item list (items the
// size cap dropped still count). The model and the golden fixtures do not carry it; it exists on API answers so a client
// can place an item without guessing, and tell an update of an omitted item from a new one (spec §8.2 client rules).
type indexedItem struct {
	convmodel.Item
	index int
}

func (i indexedItem) MarshalJSON() ([]byte, error) {
	b, err := i.Item.MarshalJSON()
	if err != nil {
		return nil, err
	}
	if len(b) < 2 || b[0] != '{' || b[len(b)-1] != '}' {
		return b, nil // not an object (cannot happen for items); leave it as it is
	}
	field := `"index":` + strconv.Itoa(i.index)
	if len(b) == 2 {
		return []byte("{" + field + "}"), nil
	}
	out := make([]byte, 0, len(b)+len(field)+2)
	out = append(out, b[:len(b)-1]...)
	out = append(out, ',')
	out = append(out, field...)
	return append(out, '}'), nil
}

// indexItems numbers items that start at position first of their turn's full list.
func indexItems(items []convmodel.Item, first int) []indexedItem {
	out := make([]indexedItem, len(items))
	for i, it := range items {
		out[i] = indexedItem{Item: it, index: first + i}
	}
	return out
}

// indexedItemsAt pairs items with the positions the entry computed for them.
func indexedItemsAt(items []convmodel.Item, at []int) []indexedItem {
	out := make([]indexedItem, len(items))
	for i, it := range items {
		out[i] = indexedItem{Item: it, index: at[i]}
	}
	return out
}

// apiTurn is convmodel.Turn on the wire with indexed items (same fields and tags; the model's own MarshalJSON would
// hide the shadowing, so the struct is spelled out).
type apiTurn struct {
	ID           string               `json:"id"`
	Index        int                  `json:"index"`
	StartedAt    int64                `json:"started_at"`
	EndedAt      *int64               `json:"ended_at,omitempty"`
	Outcome      convmodel.Outcome    `json:"outcome"`
	Error        *convmodel.TurnError `json:"error,omitempty"`
	Items        []indexedItem        `json:"items"`
	OmittedItems int                  `json:"omitted_items,omitempty"`
}

// apiTurns converts a window's turns: the first shown item of a turn that carries omitted_items N is at index N.
func apiTurns(turns []convmodel.Turn) []apiTurn {
	out := make([]apiTurn, len(turns))
	for i, t := range turns {
		out[i] = apiTurn{ID: t.ID, Index: t.Index, StartedAt: t.StartedAt, EndedAt: t.EndedAt, Outcome: t.Outcome,
			Error: t.Error, Items: indexItems(t.Items, t.OmittedItems), OmittedItems: t.OmittedItems}
	}
	return out
}

// encodeAPITurn is how a turn is encoded on the wire, for the window's size budget (the index per item included).
func encodeAPITurn(t convmodel.Turn) ([]byte, error) {
	return json.Marshal(apiTurns([]convmodel.Turn{t})[0])
}

// conversationJSON is convmodel.Conversation on the wire with indexed items.
type conversationJSON struct {
	Key          convmodel.Key           `json:"key"`
	Backend      string                  `json:"backend,omitempty"`
	Provider     string                  `json:"provider"`
	Title        string                  `json:"title"`
	Status       string                  `json:"status,omitempty"`
	Capabilities *convmodel.Capabilities `json:"capabilities,omitempty"`
	Usage        *convmodel.Usage        `json:"usage,omitempty"`
	Turns        []apiTurn               `json:"turns"`
}

var _ json.Marshaler = indexedItem{}
