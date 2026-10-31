// Package migrations owns immutable versioned runtime DDL.
package migrations

import _ "embed"

//go:embed 001_foundation.sql
var Foundation string

//go:embed 002_workflow.sql
var Workflow string

//go:embed 003_execution.sql
var Execution string
