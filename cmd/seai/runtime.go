package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"seai.local/seai/internal/application"
	"seai.local/seai/internal/contracts"
	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/maintenance"
)

func isRuntime(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "checks", "run", "provider", "agents", "release", "backup":
		return true
	case "task":
		return len(args) > 1 && (args[1] == "verify" || args[1] == "run" || args[1] == "rebase")
	}
	return false
}
func parseRuntime(args []string) (string, application.Params, extendedOptions, error) {
	var p application.Params
	var o extendedOptions
	bad := func() (string, application.Params, extendedOptions, error) {
		return "", p, o, fmt.Errorf("invalid runtime command; see docs/EXECUTION.md")
	}
	if len(args) < 2 {
		return bad()
	}
	method := ""
	rest := args[2:]
	switch args[0] + " " + args[1] {
	case "checks profiles":
		method = "checks.profiles"
	case "provider status":
		method = "provider.status"
	case "provider probe":
		method = "provider.probe"
		o.mutation = true
	case "backup create":
		method = "backup.create"
		o.mutation = true
	case "release plan":
		method = "release.plan"
		o.mutation = true
	case "release rollback":
		method = "release.rollback"
		o.mutation = true
	case "release inspect", "release apply", "run status", "run logs", "run artifact", "run cancel", "agents list":
		if len(rest) == 0 {
			return bad()
		}
		p.ID = rest[0]
		rest = rest[1:]
		method = map[string]string{"release inspect": "release.inspect", "release apply": "release.apply", "run status": "runs.get", "run logs": "runs.logs", "run artifact": "runs.artifact", "run cancel": "runs.cancel", "agents list": "agents.list"}[args[0]+" "+args[1]]
		o.mutation = method == "release.apply"
	case "checks run", "task verify", "task run", "task rebase":
		if len(rest) == 0 {
			return bad()
		}
		p.Task = rest[0]
		rest = rest[1:]
		method = map[string]string{"checks run": "checks.run", "task verify": "task.verify", "task run": "task.run", "task rebase": "task.rebase"}[args[0]+" "+args[1]]
		o.mutation = true
	default:
		return bad()
	}
	f := flag.NewFlagSet(method, flag.ContinueOnError)
	f.StringVar(&p.Artifact, "digest", "", "run-owned log or conflict artifact digest")
	f.StringVar(&p.Profile, "profile", "", "registered profile ID")
	f.StringVar(&p.Ref, "ref", "", "Git ref pinned during release planning")
	f.StringVar(&p.ReleaseID, "release-id", "", "release identity")
	f.StringVar(&p.Overlay, "overlay-policy", "", "task-only or carry for a dirty baseline")
	resolution := f.String("resolution", "", "resolution Patch JSON/YAML file")
	f.StringVar(&p.Operation, "operation-id", "", "idempotency key")
	epoch := f.Int("epoch", -1, "expected project epoch")
	f.StringVar(&p.Resume, "resume-from", "", "failed run with unchanged checkpoint")
	revision := f.Int("revision", -1, "expected task revision")
	if e := f.Parse(rest); e != nil {
		return "", p, o, e
	}
	if len(f.Args()) > 0 {
		return bad()
	}
	if *epoch >= 0 {
		p.Epoch = epoch
	}
	if *revision >= 0 {
		p.Revision = revision
	}
	if *resolution != "" {
		b, e := os.ReadFile(*resolution)
		if e != nil {
			return "", p, o, e
		}
		p.Resolution = &domain.Patch{}
		if e = contracts.Decode(b, p.Resolution); e != nil {
			return "", p, o, e
		}
	}
	allowed := map[string]bool{}
	if o.mutation {
		for _, n := range []string{"operation-id", "epoch", "revision"} {
			allowed[n] = true
		}
	}
	switch method {
	case "release.plan":
		allowed["ref"] = true
		allowed["release-id"] = true
	case "release.rollback":
		allowed["release-id"] = true
	case "runs.artifact":
		allowed["digest"] = true
	case "task.run":
		allowed["resume-from"] = true
	case "checks.run", "runs.logs":
		allowed["profile"] = true
	case "task.rebase":
		allowed["overlay-policy"] = true
		allowed["resolution"] = true
	}
	var invalid string
	f.Visit(func(v *flag.Flag) {
		if !allowed[v.Name] {
			invalid = v.Name
		}
	})
	if invalid != "" {
		return "", p, o, fmt.Errorf("flag --%s invalid for %s", invalid, method)
	}
	return method, p, o, nil
}
func offline(ctx context.Context, data string, args []string) int {
	f := flag.NewFlagSet("maintenance", flag.ContinueOnError)
	from := f.String("from", "", "verified backup directory")
	bundle := f.String("bundle", "", "operator-approved local binary bundle manifest")
	destination := f.String("output", "", "new installation directory")
	if f.Parse(args[2:]) != nil || len(f.Args()) > 0 {
		return 2
	}
	var value any
	var e error
	if args[0] == "backup" && args[1] == "restore" {
		if *from == "" {
			return 2
		}
		value, e = maintenance.Restore(ctx, *from, data)
	} else if args[0] == "upgrade" && args[1] == "prepare" {
		if *bundle == "" || *destination == "" {
			return 2
		}
		value, e = maintenance.Upgrade(ctx, data, *bundle, *destination)
	} else {
		return 2
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 4
	}
	b, _ := json.MarshalIndent(value, "", "  ")
	fmt.Println(string(b))
	return 0
}
