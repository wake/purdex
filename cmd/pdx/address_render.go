package main

// CLI address rendering: how one (address, ref) pair is shown to a person.
//
// It sits in a file of its own because it has two callers on two different
// commands — the `pdx peers` table (peers.go) and `pdx msg`'s ambiguity
// refusal (msg.go) — and keeping it in either one made the other depend on a
// command file it has nothing else to do with. Rendering an address is not
// peers-table work; the table is just one of the things that does it.

import "strings"

// addressWithRef renders an address for a human: "<host>/<name> [<ref>]".
//
// The ref prints without its leading underscore — the bracket already
// separates it — and prints on EVERY row rather than only ambiguous ones: a
// reader who has to go looking for it when a name stops working has to look
// somewhere other than where they were already reading.
//
// The bracket is omitted in TWO cases, not one. There is no ref to print
// (any row without a live cc agent), or the address is ALREADY the ref (an
// unroutable name, or no name at all) — repeating it would read as two
// different identifiers.
//
// It is one function rather than a rule written at each call site because
// `pdx peers` and `pdx msg`'s ambiguity refusal must render the same
// (address, ref) identically: the string copied out of a refusal is the
// string the table showed. Restating the rule reproduces its first arm and
// loses the second, which is exactly the case a name collision hits.
func addressWithRef(address, ref string) string {
	if ref == "" || strings.HasSuffix(address, "/"+ref) {
		return address
	}
	return address + " [" + strings.TrimPrefix(ref, "_") + "]"
}
