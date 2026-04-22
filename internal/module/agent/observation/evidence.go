package observation

// EvidenceRef is a low-cardinality key/value pair that carries supporting
// evidence for an Observation or DecisionPort. Value may be a string, int64,
// ActorKey, or any other JSON-serializable type; the caller is responsible for
// ensuring serializability.
//
// JSON unmarshal decodes numeric values as float64 (stdlib behavior). Callers
// that require the original int64 must type-assert after Unmarshal.
type EvidenceRef struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}
