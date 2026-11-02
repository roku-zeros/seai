// Package schemas contains the executable's versioned contracts for MCP discovery.
package schemas

import "embed"

//go:embed *.schema.json
var contracts embed.FS

// Contract accepts only a known contract filename, never a filesystem path.
func Contract(name string) ([]byte, error) { return contracts.ReadFile(name + "-v1.schema.json") }
