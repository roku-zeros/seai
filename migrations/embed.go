// Package migrations owns immutable versioned runtime DDL.
package migrations

import _ "embed"

//go:embed 001_foundation.sql
var Foundation string
