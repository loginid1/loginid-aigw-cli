//go:build gen_commands

package main

// JSON Schema and OpenAPI words the generator matches on.
const (
	tObject = "object"
	tArray  = "array"
	tString = "string"

	inPath   = "path"
	inQuery  = "query"
	inHeader = "header"

	// stampHeader is the one header besides Authorization the CLI sets: the updatedAt a write read.
	stampHeader = "X-Expected-Updated-At"
)
