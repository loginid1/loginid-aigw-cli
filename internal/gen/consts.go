//go:build gen_commands

package main

// JSON Schema and OpenAPI words the generator matches on.
const (
	tObject = "object"
	tArray  = "array"
	tString = "string"

	inPath  = "path"
	inQuery = "query"

	kItems                = "items"
	kProperties           = "properties"
	kAdditionalProperties = "additionalProperties"

	password = "password"
	token    = "token"
)
