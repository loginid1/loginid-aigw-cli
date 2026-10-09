//go:build gen_commands

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// rawSpec is the spec as untyped YAML, for copying JSON Schema out of it
// verbatim; the typed spec in main.go only holds what the CLI flags need.
type rawSpec map[string]any

// schemaKeys are the JSON Schema keywords an MCP input schema keeps. Examples
// are dropped: they are long, and every tool's schema is sent to the model.
var schemaKeys = []string{
	"type", "description", "enum", kItems, kProperties, "required", "default",
	"format", "minimum", "maximum", "minLength", "maxLength", "pattern",
	"minItems", "maxItems", kAdditionalProperties,
}

func loadRawSpec(path string) (rawSpec, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// A plain map, not rawSpec: yaml.v3 decodes nested mappings into the
	// target's own map type, which would defeat every map[string]any check.
	var raw map[string]any

	err = yaml.Unmarshal(b, &raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return rawSpec(raw), nil
}

func (r rawSpec) operation(path, method string) (map[string]any, error) {
	paths, _ := r["paths"].(map[string]any)
	item, _ := paths[path].(map[string]any)

	op, ok := item[method].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("no %s %s in the raw spec", method, path)
	}

	return op, nil
}

// inline returns node with every $ref replaced by its target and only
// schemaKeys kept, so the result stands alone outside the spec.
func (r rawSpec) inline(node any, depth int) (any, error) {
	if depth > 20 {
		return nil, errors.New("schema nests too deeply; is it recursive?")
	}

	switch n := node.(type) {
	case map[string]any:
		if ref, ok := n["$ref"].(string); ok {
			name, found := strings.CutPrefix(ref, "#/components/schemas/")
			if !found {
				return nil, fmt.Errorf("unsupported $ref %q", ref)
			}

			components, _ := r["components"].(map[string]any)
			schemas, _ := components["schemas"].(map[string]any)

			target, ok := schemas[name]
			if !ok {
				return nil, fmt.Errorf("unknown $ref %q", ref)
			}

			return r.inline(target, depth+1)
		}

		// Composition is refused, not dropped: dropping it would leave an
		// empty schema, so secret detection would see nothing and the MCP
		// input check would reject every field.
		for _, k := range []string{"allOf", "oneOf", "anyOf", "not"} {
			if _, ok := n[k]; ok {
				return nil, fmt.Errorf("schema keyword %q is not supported; extend internal/gen before using it", k)
			}
		}

		out := map[string]any{}

		for _, k := range schemaKeys {
			v, ok := n[k]
			if !ok {
				continue
			}

			if k == kProperties {
				props, _ := v.(map[string]any)
				inlined := map[string]any{}

				for name, p := range props {
					ip, err := r.inline(p, depth+1)
					if err != nil {
						return nil, fmt.Errorf("%s: %w", name, err)
					}

					inlined[name] = ip
				}

				out[k] = inlined

				continue
			}

			iv, err := r.inline(v, depth+1)
			if err != nil {
				return nil, err
			}

			out[k] = iv
		}

		// Closed objects all the way down, not just at the top: otherwise a
		// typo inside a nested object ("exchange", "scopes[]") would pass
		// the MCP tool's input check and be sent.
		if _, hasProps := out[kProperties]; hasProps {
			if _, set := out[kAdditionalProperties]; !set {
				out[kAdditionalProperties] = false
			}
		}

		return out, nil
	case []any:
		out := make([]any, len(n))

		for i, v := range n {
			iv, err := r.inline(v, depth+1)
			if err != nil {
				return nil, err
			}

			out[i] = iv
		}

		return out, nil
	default:
		return n, nil
	}
}

// inputSchema is the MCP tool input for one operation: path and query
// parameters next to the body's own top-level fields, in one flat object, as
// the model would naturally write them. A clash between the two is an error,
// since the server could not tell them apart.
func (r rawSpec) inputSchema(path, method string) (string, error) {
	op, err := r.operation(path, method)
	if err != nil {
		return "", err
	}

	props := map[string]any{}

	var required []string

	params, _ := op["parameters"].([]any)
	for _, p := range params {
		pm, _ := p.(map[string]any)

		in, _ := pm["in"].(string)
		if in != inPath && in != inQuery {
			continue
		}

		name, _ := pm["name"].(string)

		s, inlineErr := r.inline(pm["schema"], 0)
		if inlineErr != nil {
			return "", fmt.Errorf("parameter %s: %w", name, inlineErr)
		}

		sm, _ := s.(map[string]any)
		if desc, ok := pm["description"].(string); ok && desc != "" {
			sm["description"] = desc
		}

		props[name] = sm

		if req, _ := pm["required"].(bool); req || in == inPath {
			required = append(required, name)
		}
	}

	if rb, ok := op["requestBody"].(map[string]any); ok {
		content, _ := rb["content"].(map[string]any)
		media, _ := content["application/json"].(map[string]any)

		body, inlineErr := r.inline(media["schema"], 0)
		if inlineErr != nil {
			return "", fmt.Errorf("request body: %w", inlineErr)
		}

		bm, _ := body.(map[string]any)
		bodyProps, _ := bm[kProperties].(map[string]any)

		for name, p := range bodyProps {
			if _, clash := props[name]; clash {
				return "", fmt.Errorf("body field %q clashes with a parameter of the same name", name)
			}

			props[name] = p
		}

		if bodyRequired, _ := rb["required"].(bool); bodyRequired {
			required = append(required, anyStrings(bm["required"])...)
		}
	}

	schema := map[string]any{"type": tObject, kProperties: props, kAdditionalProperties: false}

	if len(required) > 0 {
		slices.Sort(required)
		schema["required"] = required
	}

	// json.Marshal sorts map keys, so the same spec gives the same bytes.
	b, err := json.Marshal(schema)
	if err != nil {
		return "", err
	}

	return string(b), nil
}

// returnsSecret reports whether a success response carries a string field
// named like a secret. Those are one-time values: the API never shows them again.
func (r rawSpec) returnsSecret(path, method string) (bool, error) {
	op, err := r.operation(path, method)
	if err != nil {
		return false, err
	}

	responses, _ := op["responses"].(map[string]any)

	for code, resp := range responses {
		if !strings.HasPrefix(code, "2") {
			continue
		}

		rm, _ := resp.(map[string]any)
		content, _ := rm["content"].(map[string]any)
		media, _ := content["application/json"].(map[string]any)

		if media["schema"] == nil {
			continue
		}

		s, err := r.inline(media["schema"], 0)
		if err != nil {
			return false, fmt.Errorf("response %s: %w", code, err)
		}

		if hasSecretField(s) {
			return true, nil
		}
	}

	return false, nil
}

// secretSuffixes end the names of fields that hold credentials. Suffixes,
// not substrings: "tokenEndpoint" and "subjectTokenType" are not secrets.
var secretSuffixes = []string{"secret", "secrets", token, "tokens", password, "privatekey", "apikey", "credential", "credentials"}

func hasSecretField(node any) bool {
	m, ok := node.(map[string]any)
	if !ok {
		return false
	}

	props, _ := m[kProperties].(map[string]any)
	for name, p := range props {
		pm, _ := p.(map[string]any)
		if (isSecretName(name) && holdsText(p)) || pm["format"] == password {
			return true
		}

		if hasSecretField(p) {
			return true
		}
	}

	return hasSecretField(m[kItems]) || hasSecretField(m[kAdditionalProperties])
}

func isSecretName(name string) bool {
	n := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(name))
	for _, suffix := range secretSuffixes {
		if strings.HasSuffix(n, suffix) {
			return true
		}
	}

	return false
}

// holdsText reports whether a field can carry a secret value: a string, a
// list of them, or anything marked format: password. Not a boolean such as
// hasClientSecret.
func holdsText(node any) bool {
	m, _ := node.(map[string]any)
	if m["format"] == password {
		return true
	}

	switch t := m["type"].(type) {
	case string:
		if t == tArray {
			return holdsText(m[kItems])
		}

		return t == tString
	case []any:
		for _, v := range t {
			if v == tString {
				return true
			}
		}
	}

	return false
}

func anyStrings(v any) []string {
	list, _ := v.([]any)

	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}

	return out
}
