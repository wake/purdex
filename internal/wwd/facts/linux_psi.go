package facts

import "github.com/wake/purdex/internal/wwd/wire"

// MergePSI is the larger of the production slices' pressure per field. Every
// text must parse: an empty string stands for a file that could not be read. If
// any slice is missing the result is nil (unknown), because the missing one may
// be the slice under pressure and the others' zeros would hide it.
func MergePSI(texts []string) *wire.PSI {
	out := &wire.PSI{}
	for _, text := range texts {
		s10, s60, f10, err := ParsePSI(text)
		if err != nil {
			return nil
		}
		out.Some10, out.Some60, out.Full10 = max(out.Some10, s10), max(out.Some60, s60), max(out.Full10, f10)
	}
	return out
}
