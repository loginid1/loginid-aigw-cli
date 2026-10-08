package apicmd

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// applyData sets one -d key=value entry on root. Keys use Stripe's bracket
// form: "name", "exchange[audience]", "scopes[0][value]". The value is typed
// and checked from op, and a key the spec does not know is an error, so a typo fails
// here rather than as a confusing 400.
func applyData(root any, op *Operation, entry string) (any, error) {
	key, value, ok := strings.Cut(entry, "=")
	if !ok {
		return nil, errors.New("want key=value")
	}

	segs, err := splitKey(key)
	if err != nil {
		return nil, err
	}

	path := normalize(segs)

	typ, ok := op.Fields[path]
	if !ok {
		return nil, fmt.Errorf("unknown field %q", key)
	}

	if enum := op.FieldEnums[path]; len(enum) > 0 && !slices.Contains(enum, value) {
		return nil, fmt.Errorf("%q is not one of: %s", value, enumList(enum))
	}

	v, err := coerce(typ, value)
	if err != nil {
		return nil, err
	}

	return insert(root, segs, v)
}

// splitKey turns "a[b][0]" into ["a", "b", "0"].
func splitKey(key string) ([]string, error) {
	head, rest, _ := strings.Cut(key, "[")
	if head == "" {
		return nil, fmt.Errorf("malformed key %q", key)
	}

	segs := []string{head}

	for rest != "" {
		seg, after, ok := strings.Cut(rest, "]")
		if !ok || seg == "" {
			return nil, fmt.Errorf("malformed key %q", key)
		}

		segs = append(segs, seg)

		if after == "" {
			break
		}

		if !strings.HasPrefix(after, "[") {
			return nil, fmt.Errorf("malformed key %q", key)
		}

		rest = after[1:]
	}

	return segs, nil
}

// normalize turns ["scopes", "0", "value"] into "scopes[].value", the form the
// generated Fields table uses.
func normalize(segs []string) string {
	var b strings.Builder

	for i, s := range segs {
		switch {
		case isIndex(s):
			b.WriteString("[]")
		case i > 0:
			b.WriteByte('.')
			b.WriteString(s)
		default:
			b.WriteString(s)
		}
	}

	return b.String()
}

// isIndex accepts plain decimal indexes only: "0", "12", never "00", "+0" or
// "-1". Otherwise scopes[0] and scopes[00] would quietly name the same item.
func isIndex(s string) bool {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return false
	}

	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}

func coerce(typ, raw string) (any, error) {
	switch typ {
	case TypeBoolean:
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("%q is not a boolean", raw)
		}

		return v, nil
	case TypeInteger:
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not an integer", raw)
		}

		return v, nil
	case TypeNumber:
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", raw)
		}

		return v, nil
	default:
		return raw, nil
	}
}

// insert sets value at segs inside node, creating maps for names and slices for
// numeric indexes, and returns the updated node.
func insert(node any, segs []string, value any) (any, error) {
	if len(segs) == 0 {
		return value, nil
	}

	seg, rest := segs[0], segs[1:]

	if isIndex(seg) {
		list, ok := node.([]any)
		if node != nil && !ok {
			return nil, fmt.Errorf("index [%s] used on a field that is not a list", seg)
		}

		// Items are added in order, one at a time: an index past the end
		// would leave a gap, and a huge one would allocate all of it first.
		idx, err := strconv.Atoi(seg)
		if err != nil || idx > len(list) {
			return nil, fmt.Errorf("index [%s] skips [%d]: set list items in order, starting at [0]", seg, len(list))
		}

		if idx == len(list) {
			list = append(list, nil)
		}

		v, err := insert(list[idx], rest, value)
		if err != nil {
			return nil, err
		}

		list[idx] = v

		return list, nil
	}

	obj, ok := node.(map[string]any)
	if node != nil && !ok {
		return nil, fmt.Errorf("field %q set on a value that is not an object", seg)
	}

	if obj == nil {
		obj = map[string]any{}
	}

	v, err := insert(obj[seg], rest, value)
	if err != nil {
		return nil, err
	}

	obj[seg] = v

	return obj, nil
}
