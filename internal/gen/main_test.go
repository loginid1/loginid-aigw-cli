//go:build gen_commands

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureSpec = `
openapi: 3.0.3
servers: [{url: "https://api.example"}]
paths:
  /v1/things/{thingId}/parts/{partId}:
    get:
      operationId: things#getPart
      x-required-scope: things:read
      description: One part. Second sentence.
      parameters:
        - {name: thingId, in: path, required: true, schema: {type: string}}
        - {name: partId, in: path, required: true, schema: {type: string}}
  /v1/things:
    post:
      operationId: things#create
      x-required-scope: things:write
      description: Creates a thing.
      requestBody:
        required: true
        content:
          application/json:
            schema: {$ref: "#/components/schemas/Thing"}
components:
  schemas:
    Thing:
      type: object
      required: [name]
      properties:
        name: {type: string}
        child: {$ref: "#/components/schemas/Thing"}
`

func write(t *testing.T, name, body string) string {
	t.Helper()

	p := filepath.Join(t.TempDir(), name)

	err := os.WriteFile(p, []byte(body), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return p
}

func TestGenerateChecksTheOverlay(t *testing.T) {
	t.Parallel()

	// No recursion here, so the success case can build.
	flat := strings.Replace(fixtureSpec, "        child: {$ref: \"#/components/schemas/Thing\"}\n", "", 1)
	spec := write(t, "spec.yaml", flat)

	ok := write(t, "ok.yaml", "operations:\n  things#getPart: {command: things parts get}\n  things#create: {command: things create}\n")

	src, err := generate(spec, ok, "test")
	if err != nil {
		t.Fatalf("valid overlay: %v", err)
	}

	// The ID the path ends with is positional; the parent ID is a flag.
	out := string(src)
	if !strings.Contains(out, `Args: []Param{
			{Name: "partId"`) || !strings.Contains(out, `Flag: "thing-id"`) {
		t.Errorf("positional/flag split wrong:\n%s", out)
	}

	missing := write(t, "missing.yaml", "operations:\n  things#getPart: {command: things parts get}\n")

	_, err = generate(spec, missing, "test")
	if err == nil || !strings.Contains(err.Error(), `"things#create"`) {
		t.Errorf("an operation with no overlay entry: %v", err)
	}

	extra := write(t, "extra.yaml", "operations:\n  things#getPart: {command: things parts get}\n  things#create: {command: things create}\n  things#gone: {command: things gone}\n")

	_, err = generate(spec, extra, "test")
	if err == nil || !strings.Contains(err.Error(), `"things#gone"`) {
		t.Errorf("an overlay entry with no operation: %v", err)
	}

	dup := write(t, "dup.yaml", "operations:\n  things#getPart: {command: things create}\n  things#create: {command: things create}\n")

	_, err = generate(spec, dup, "test")
	if err == nil || !strings.Contains(err.Error(), "used by both") {
		t.Errorf("two operations, one command: %v", err)
	}
}

func TestGenerateRefusesRecursiveSchemas(t *testing.T) {
	t.Parallel()

	spec := write(t, "spec.yaml", fixtureSpec)
	ov := write(t, "ov.yaml", "operations:\n  things#getPart: {command: things parts get}\n  things#create: {command: things create}\n")

	_, err := generate(spec, ov, "test")
	if err == nil || !strings.Contains(err.Error(), "recursive") {
		t.Errorf("a self-referencing schema: %v", err)
	}
}

func TestResponseSecretsFindsTopLevelSecretFields(t *testing.T) {
	t.Parallel()

	s := &spec{}
	s.Components.Schemas = map[string]*schema{
		"Created": {Type: tObject, Properties: map[string]*schema{
			"id":           {Type: tString},
			"clientSecret": {Type: tString},
		}},
		"Nested": {Type: tObject, Properties: map[string]*schema{
			"inner": {Ref: "#/components/schemas/Created"},
		}},
	}

	op := func(code, ref string) *operation {
		o := &operation{}
		o.Responses = map[string]struct {
			Content map[string]struct{ Schema *schema }
		}{code: {Content: map[string]struct{ Schema *schema }{
			"application/json": {Schema: &schema{Ref: "#/components/schemas/" + ref}},
		}}}

		return o
	}

	for name, tc := range map[string]struct {
		o    *operation
		want string
	}{
		"top level, 201": {op("201", "Created"), "clientSecret"},
		"top level, 200": {op("200", "Created"), "clientSecret"},
		"error response": {op("400", "Created"), ""},
		"nested only":    {op("201", "Nested"), ""},
		"no response":    {&operation{}, ""},
	} {
		got, err := responseSecrets(s, tc.o)
		if err != nil || strings.Join(got, ",") != tc.want {
			t.Errorf("%s: %v, %v; want %q", name, got, err, tc.want)
		}
	}
}

func TestAPIBasePrefersTheOverlay(t *testing.T) {
	t.Parallel()

	s := &spec{Servers: []struct{ URL string }{{URL: "https://from-spec.example"}}}

	for name, tc := range map[string]struct {
		s      *spec
		server string
		want   string
		ok     bool
	}{
		"spec only":    {s, "", "https://from-spec.example", true},
		"overlay wins": {s, "https://api.example/", "https://api.example", true},
		"neither":      {&spec{}, "", "", false},
		"plain http":   {s, "http://api.example", "", false},
		"a path":       {s, "https://api.example/v1", "", false},
		"no host":      {s, "https://", "", false},
		"query":        {s, "https://api.example?x=1", "", false},
	} {
		got, err := apiBase(tc.s, &overlay{Server: tc.server})
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("%s: %q, %v", name, got, err)
		}
	}
}
