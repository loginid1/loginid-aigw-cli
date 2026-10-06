package apicmd

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// applyData sets one -d key=value entry on root. Keys use Stripe's bracket
// form: "name", "exchange[audience]", "scopes[0][value]". The value is typed
// from fields, and a key the spec does not know is an error, so a typo fails
// here rather than as a confusing 400.
func applyData(root any, fields map[string]string, entry string) (any, error) {
	key, value, ok := strings.Cut(entry, "=")
	if !ok {
		return nil, errors.New("want key=value")
	}

	segs, err := splitKey(key)
	if err != nil {
		return nil, err
	}

	typ, ok := fields[normalize(segs)]
	if !ok {
		return nil, fmt.Errorf("unknown field %q", key)
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

func isIndex(s string) bool {
	_, err := strconv.Atoi(s)

	return err == nil
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

	idx, convErr := strconv.Atoi(seg)
	if convErr == nil {
		if idx < 0 {
			return nil, fmt.Errorf("negative index %d", idx)
		}

		list, ok := node.([]any)
		if node != nil && !ok {
			return nil, fmt.Errorf("index [%d] used on a field that is not a list", idx)
		}

		for len(list) <= idx {
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

// checkNoHoles rejects lists with skipped indexes, such as scopes[1] set
// without scopes[0], which would otherwise send a null the API never asked for.
func checkNoHoles(node any) error {
	switch n := node.(type) {
	case map[string]any:
		for k, v := range n {
			err := checkNoHoles(v)
			if err != nil {
				return fmt.Errorf("%s%w", k, err)
			}
		}
	case []any:
		for i, v := range n {
			if v == nil {
				return fmt.Errorf("[%d]: index skipped", i)
			}

			err := checkNoHoles(v)
			if err != nil {
				return fmt.Errorf("[%d]%w", i, err)
			}
		}
	}

	return nil
}
