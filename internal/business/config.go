// Package business extracts auditable domain registrations using Go AST/types.
package business

import (
	"fmt"
	"regexp"
	"seai.local/seai/internal/domain"
	"strings"
)

const Version = "business-v1"

func Validate(c *domain.BusinessConfig) error {
	if c == nil {
		return nil
	}
	if c.Schema != "seai.business/v1" || len(c.Adapters) > 32 || len(c.Requirements) > 1000 {
		return domain.Err("SCHEMA_INVALID", "business config schema or collection limits")
	}
	seen := map[string]bool{}
	ident := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	for _, a := range c.Adapters {
		identity := a.Kind + ":" + a.Package + ":" + a.Symbol
		if !regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`).MatchString(a.Name) || seen[a.Name] || seen[identity] || a.Package == "" || a.Symbol == "" || strings.ContainsAny(a.Package+a.Symbol, "\x00\r\n") {
			return domain.Err("SCHEMA_INVALID", "unique adapter names and typed identities required")
		}
		seen[a.Name] = true
		seen[identity] = true
		if a.Kind == "struct" {
			if !ident.MatchString(a.Symbol) || !ident.MatchString(a.IDField) || len(a.Handlers) == 0 || len(a.Handlers) > 8 || len(a.ContextFields) > 16 || len(a.HandlerArgs) > 0 || len(a.ContextArgs) > 0 || a.PriorityArg != nil || a.IDArg != 0 {
				return domain.Err("SCHEMA_INVALID", "invalid struct adapter")
			}
			for role, field := range a.Handlers {
				if !ident.MatchString(role) || !ident.MatchString(field) {
					return domain.Err("SCHEMA_INVALID", "handler role/field must be identifiers")
				}
			}
			for _, f := range a.ContextFields {
				if !ident.MatchString(f) {
					return domain.Err("SCHEMA_INVALID", "context field")
				}
			}
			if a.PriorityField != "" && !ident.MatchString(a.PriorityField) {
				return domain.Err("SCHEMA_INVALID", "priority field")
			}
		} else if a.Kind == "call" {
			parts := strings.Split(a.Symbol, ".")
			if len(parts) > 2 {
				return domain.Err("SCHEMA_INVALID", "call symbol is Function or Receiver.Method")
			}
			for _, part := range parts {
				if !ident.MatchString(part) {
					return domain.Err("SCHEMA_INVALID", "invalid call symbol")
				}
			}
			if a.IDArg < 0 || a.IDArg > 63 || len(a.HandlerArgs) == 0 || len(a.HandlerArgs) > 8 || len(a.ContextArgs) > 16 || a.IDField != "" || len(a.Handlers) > 0 || len(a.ContextFields) > 0 || a.PriorityField != "" {
				return domain.Err("SCHEMA_INVALID", "invalid call adapter")
			}
			for role, n := range a.HandlerArgs {
				if !ident.MatchString(role) || n < 0 || n > 63 {
					return domain.Err("SCHEMA_INVALID", "invalid handler argument")
				}
			}
			for name, n := range a.ContextArgs {
				if !ident.MatchString(name) || n < 0 || n > 63 {
					return domain.Err("SCHEMA_INVALID", "invalid context argument")
				}
			}
			if a.PriorityArg != nil && (*a.PriorityArg < 0 || *a.PriorityArg > 63) {
				return domain.Err("SCHEMA_INVALID", "priority arg")
			}
		} else if a.Kind == "function" {
			if a.IDField != "" || len(a.Handlers) > 0 || len(a.HandlerArgs) > 0 || len(a.ContextArgs) > 0 || len(a.ContextFields) > 0 || a.PriorityField != "" || a.PriorityArg != nil || a.IDArg != 0 {
				return domain.Err("SCHEMA_INVALID", "function adapter only accepts name/package/symbol")
			}
			parts := strings.Split(a.Symbol, ".")
			if len(parts) > 2 {
				return domain.Err("SCHEMA_INVALID", "function symbol")
			}
			for _, part := range parts {
				if !ident.MatchString(part) {
					return domain.Err("SCHEMA_INVALID", "function symbol")
				}
			}
		} else {
			return domain.Err("SCHEMA_INVALID", "adapter kind must be struct, call or function")
		}
	}
	if c.RequireForChanges && len(c.Adapters) == 0 {
		return domain.Err("SCHEMA_INVALID", "require_for_changes needs adapters")
	}
	for _, r := range c.Requirements {
		if strings.TrimSpace(r.RuleKey) == "" || r.ExternalID == "" || r.Revision == "" || r.Content == "" || len(r.Content) > 65536 || domain.Digest([]byte(r.Content)) != r.ContentDigest {
			return fmt.Errorf("business requirement needs rule key, ID, revision and exact content hash")
		}
	}
	return nil
}
func Digest(c *domain.BusinessConfig) string { b, _ := domain.Canonical(c); return domain.Digest(b) }
