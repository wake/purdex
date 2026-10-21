package main

// The path and value half of `pdx watch wake-settings set` (spec §5): `rules.patrol.times[0]=…` read as a step through the document and a
// value typed by what is there now. Moved out of watch_wake_settings.go unchanged.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// A step of a path: a key of an object, then any number of [i] of an array.
type wakeStep struct {
	key string
	idx []int
}

// parseWakePath reads `rules.patrol.times[0]` the way the spec writes a path: keys with dots, and [i] after a key for an element of an array.
func parseWakePath(path string) ([]wakeStep, error) {
	var steps []wakeStep
	for _, part := range strings.Split(path, ".") {
		key, rest, _ := strings.Cut(part, "[")
		if key == "" {
			return nil, fmt.Errorf("empty name in the path")
		}
		st := wakeStep{key: key}
		for rest != "" || strings.Contains(part, "[") && st.idx == nil {
			end := strings.Index(rest, "]")
			if end < 0 {
				return nil, fmt.Errorf("[ without ]")
			}
			n, err := strconv.Atoi(rest[:end])
			if err != nil || n < 0 {
				return nil, fmt.Errorf("[%s] is not an index", rest[:end])
			}
			st.idx = append(st.idx, n)
			rest = rest[end+1:]
			if rest == "" {
				break
			}
			if !strings.HasPrefix(rest, "[") {
				return nil, fmt.Errorf("%q after ]", rest)
			}
			rest = rest[1:]
		}
		steps = append(steps, st)
	}
	return steps, nil
}

// applyWakePath sets the leaf at path in doc to the value in raw, read as the type the leaf has now (so a path that is not in the document
// is an error here, not a 400 later): bool, whole number, string (`@file` reads the file, `@@` is a literal @), or for an array a comma list.
func applyWakePath(doc map[string]any, path, raw string) error {
	steps, err := parseWakePath(path)
	if err != nil {
		return err
	}
	var cur any = doc
	for i, st := range steps {
		obj, ok := cur.(map[string]any)
		if !ok {
			return fmt.Errorf("%s is not an object", strings.Join(wakeNames(steps[:i]), "."))
		}
		child, ok := obj[st.key]
		if !ok {
			return fmt.Errorf("no such path (%s is not in the document)", st.key)
		}
		last := i == len(steps)-1
		// indexes into arrays
		for j, ix := range st.idx {
			arr, ok := child.([]any)
			if !ok {
				return fmt.Errorf("%s is not an array", st.key)
			}
			if ix >= len(arr) {
				return fmt.Errorf("index %d is past the end of %s (%d elements); set the whole array to add", ix, st.key, len(arr))
			}
			if last && j == len(st.idx)-1 {
				v, err := wakeScalar(arr[ix], raw)
				if err != nil {
					return err
				}
				arr[ix] = v
				return nil
			}
			child = arr[ix]
		}
		if last {
			v, err := wakeLeaf(child, raw)
			if err != nil {
				return err
			}
			obj[st.key] = v
			return nil
		}
		cur = child
	}
	return fmt.Errorf("empty path")
}

func wakeNames(steps []wakeStep) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.key
	}
	return out
}

// wakeLeaf reads raw as the type of old.
func wakeLeaf(old any, raw string) (any, error) {
	switch old.(type) {
	case []any:
		if raw == "" {
			return []any{}, nil // an empty list, not null
		}
		parts := strings.Split(raw, ",")
		out := make([]any, len(parts))
		for i, p := range parts {
			out[i] = strings.TrimSpace(p)
		}
		return out, nil
	case map[string]any:
		return nil, fmt.Errorf("is an object: name a field of it")
	}
	return wakeScalar(old, raw)
}

// wakeScalar reads raw as the type of old, a bool, a whole number or a string.
func wakeScalar(old any, raw string) (any, error) {
	switch old.(type) {
	case bool:
		switch raw {
		case "true", "on", "yes":
			return true, nil
		case "false", "off", "no":
			return false, nil
		}
		return nil, fmt.Errorf("%q is not true or false", raw)
	case json.Number:
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("%q is not a whole number", raw)
		}
		return json.Number(strconv.Itoa(n)), nil
	case string:
		if strings.HasPrefix(raw, "@@") {
			return raw[1:], nil
		}
		if strings.HasPrefix(raw, "@") {
			return readWakeFile(raw[1:])
		}
		return raw, nil
	}
	return nil, fmt.Errorf("cannot be set from the command line")
}

// wakeFileMax is the most a `@file` value may be: what the daemon takes in a whole PUT (64 KiB). The daemon's own limit on a prompt (2000
// bytes) is its to apply and to explain; this one only keeps a wrong file from being read into memory.
const wakeFileMax = 64 << 10

// readWakeFile reads a value from a file, at most wakeFileMax bytes: one byte more is read to know it is over.
func readWakeFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("cannot read the file: %v", err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, wakeFileMax+1))
	if err != nil {
		return "", fmt.Errorf("cannot read the file: %v", err)
	}
	if len(b) > wakeFileMax {
		return "", fmt.Errorf("the file is too large (over %d bytes)", wakeFileMax)
	}
	return string(b), nil
}
