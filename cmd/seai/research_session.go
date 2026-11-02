package main

import (
	"flag"
	"fmt"
	"os"
	"seai.local/seai/internal/application"
	"seai.local/seai/internal/contracts"
	"seai.local/seai/internal/domain"
)

type researchTopics []string

func (t *researchTopics) String() string     { return fmt.Sprint([]string(*t)) }
func (t *researchTopics) Set(v string) error { *t = append(*t, v); return nil }
func parseResearch(args []string) (string, application.Params, extendedOptions, error) {
	var p application.Params
	var o extendedOptions
	if len(args) < 2 {
		return "", p, o, fmt.Errorf("research start/status/claim/report/cancel required")
	}
	method := "research." + args[1]
	rest := args[2:]
	if args[1] != "start" {
		if len(rest) == 0 {
			return "", p, o, fmt.Errorf("research run ID required")
		}
		p.ID = rest[0]
		rest = rest[1:]
	}
	f := flag.NewFlagSet(method, flag.ContinueOnError)
	objective := f.String("objective", "", "research question")
	var topics researchTopics
	f.Var(&topics, "topic", "independent topic (repeat 1..6 times)")
	f.StringVar(&p.Generation, "generation", "", "explicit immutable index generation")
	f.StringVar(&p.Operation, "operation-id", "", "idempotency key")
	f.StringVar(&p.ResearchJob, "job", "", "research job ID")
	f.IntVar(&p.Lease, "lease", 0, "lease_epoch received from claim")
	epoch := f.Int("epoch", -1, "expected project epoch")
	report := f.String("report", "", "JSON report file")
	if e := f.Parse(rest); e != nil {
		return "", p, o, e
	}
	if len(f.Args()) != 0 {
		return "", p, o, fmt.Errorf("unexpected arguments")
	}
	allowed := map[string]bool{}
	switch args[1] {
	case "start":
		o.mutation = true
		for _, k := range []string{"objective", "topic", "generation", "operation-id", "epoch"} {
			allowed[k] = true
		}
		p.Research = &domain.ResearchRequest{Objective: *objective, Topics: topics}
	case "status":
		method = "research.get"
	case "claim":
		o.mutation = true
		allowed["job"] = true
		allowed["operation-id"] = true
	case "report":
		o.mutation = true
		for _, k := range []string{"job", "lease", "report", "operation-id"} {
			allowed[k] = true
		}
		b, e := os.ReadFile(*report)
		if e != nil {
			return "", p, o, e
		}
		p.Report = &domain.ResearchReport{}
		if e = contracts.Decode(b, p.Report); e != nil {
			return "", p, o, e
		}
	case "cancel":
	default:
		return "", p, o, fmt.Errorf("unsupported research command")
	}
	var bad string
	f.Visit(func(v *flag.Flag) {
		if !allowed[v.Name] {
			bad = v.Name
		}
	})
	if bad != "" {
		return "", p, o, fmt.Errorf("flag --%s invalid for %s", bad, method)
	}
	if *epoch >= 0 {
		p.Epoch = epoch
	}
	return method, p, o, nil
}
