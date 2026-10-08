//go:build gen_commands

package main

import (
	"strings"
	"testing"
)

func TestHasSecretField(t *testing.T) {
	t.Parallel()

	str := map[string]any{"type": "string"}
	obj := func(props map[string]any) map[string]any {
		return map[string]any{"type": "object", "properties": props}
	}

	cases := []struct {
		name   string
		schema any
		want   bool
	}{
		{"clientSecret", obj(map[string]any{"clientSecret": str}), true},
		{"accessToken", obj(map[string]any{"accessToken": str}), true},
		{"private_key", obj(map[string]any{"private_key": str}), true},
		{"apiKey", obj(map[string]any{"apiKey": str}), true},
		{"password", obj(map[string]any{"password": str}), true},
		{"format password", obj(map[string]any{"value": map[string]any{"type": "string", "format": "password"}}), true},
		{"list of secrets", obj(map[string]any{"clientSecrets": map[string]any{"type": "array", "items": str}}), true},
		{"nullable 3.1 type", obj(map[string]any{"clientSecret": map[string]any{"type": []any{"string", "null"}}}), true},
		{"nested", obj(map[string]any{"resource": obj(map[string]any{"clientSecret": str})}), true},
		{"in a list item", map[string]any{"type": "array", "items": obj(map[string]any{"token": str})}, true},
		{"in additionalProperties", map[string]any{"type": "object", "additionalProperties": obj(map[string]any{"token": str})}, true},

		// Not secrets.
		{"boolean flag", obj(map[string]any{"hasClientSecret": map[string]any{"type": "boolean"}}), false},
		{"tokenEndpoint", obj(map[string]any{"tokenEndpoint": str}), false},
		{"subjectTokenType", obj(map[string]any{"subjectTokenType": str}), false},
		{"plain fields", obj(map[string]any{"name": str, "id": str}), false},
	}

	for _, tc := range cases {
		if got := hasSecretField(tc.schema); got != tc.want {
			t.Errorf("%s: hasSecretField = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestInlineClosesObjectsAndRefusesComposition(t *testing.T) {
	t.Parallel()

	r := rawSpec{"components": map[string]any{"schemas": map[string]any{
		"Inner": map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}},
		"Mixed": map[string]any{"allOf": []any{map[string]any{"$ref": "#/components/schemas/Inner"}}},
		"Self":  map[string]any{"type": "object", "properties": map[string]any{"child": map[string]any{"$ref": "#/components/schemas/Self"}}},
	}}}

	got, err := r.inline(map[string]any{"type": "object", "properties": map[string]any{
		"inner": map[string]any{"$ref": "#/components/schemas/Inner"},
	}}, 0)
	if err != nil {
		t.Fatal(err)
	}

	inner := got.(map[string]any)["properties"].(map[string]any)["inner"].(map[string]any)
	if inner["additionalProperties"] != false {
		t.Errorf("nested object left open: %v", inner)
	}

	_, err = r.inline(map[string]any{"$ref": "#/components/schemas/Mixed"}, 0)
	if err == nil || !strings.Contains(err.Error(), "allOf") {
		t.Errorf("allOf: want a refusal, got %v", err)
	}

	_, err = r.inline(map[string]any{"$ref": "#/components/schemas/Self"}, 0)
	if err == nil {
		t.Error("a recursive schema was inlined")
	}
}
