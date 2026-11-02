package main

import (
	"flag"
	"fmt"
	"seai.local/seai/internal/application"
	"strings"
)

type contextFlags map[string]string

func (c *contextFlags) String() string { return fmt.Sprint(map[string]string(*c)) }
func (c *contextFlags) Set(v string) error {
	k, val, ok := strings.Cut(v, "=")
	if !ok || k == "" || val == "" {
		return fmt.Errorf("context must be Name=Value")
	}
	if *c == nil {
		*c = map[string]string{}
	}
	if _, ok := (*c)[k]; ok {
		return fmt.Errorf("duplicate context key")
	}
	(*c)[k] = val
	return nil
}
func parseBusiness(args []string) (string, application.Params, extendedOptions, error) {
	var p application.Params
	var o extendedOptions
	bad := func() (string, application.Params, extendedOptions, error) {
		return "", p, o, fmt.Errorf("usage: rules discover|diagnostics|search QUERY|show ID; flow ID; impact ID [--depth 3 --limit 50 --generation ID --context Product=X]")
	}
	if len(args) < 2 {
		return bad()
	}
	method := ""
	rest := []string{}
	if args[0] == "rules" {
		switch args[1] {
		case "discover", "diagnostics":
			method = "rules." + args[1]
			rest = args[2:]
		case "search", "show":
			if len(args) < 3 {
				return bad()
			}
			method = "rules.search"
			if args[1] == "show" {
				method = "rules.get"
			}
			p.Query = args[2]
			rest = args[3:]
		default:
			return bad()
		}
	} else {
		method = args[0] + ".get"
		p.Query = args[1]
		rest = args[2:]
	}
	f := flag.NewFlagSet(method, flag.ContinueOnError)
	f.StringVar(&p.Generation, "generation", "", "immutable index generation")
	f.IntVar(&p.Limit, "limit", 0, "result/node limit")
	f.IntVar(&p.Offset, "offset", 0, "page offset")
	depth := f.Int("depth", 0, "graph depth (default 3, max 8)")
	scope := contextFlags{}
	f.Var(&scope, "context", "exact context Name=Value, repeatable")
	if e := f.Parse(rest); e != nil {
		return "", p, o, e
	}
	if len(f.Args()) > 0 {
		return bad()
	}
	if *depth != 0 && method != "flow.get" && method != "impact.get" {
		return bad()
	}
	p.Depth = *depth
	p.Context = scope
	return method, p, o, nil
}
