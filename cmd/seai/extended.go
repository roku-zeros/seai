package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"seai.local/seai/internal/application"
	"seai.local/seai/internal/contracts"
	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/transports"
)

type extendedOptions struct {
	mutation, human bool
	output, format  string
}

func parseExtended(args []string) (string, application.Params, extendedOptions, error) {
	if isRuntime(args) {
		return parseRuntime(args)
	}
	var p application.Params
	var o extendedOptions
	method := ""
	rest := []string{}
	bad := func() (string, application.Params, extendedOptions, error) {
		return "", p, o, fmt.Errorf("unsupported command/arguments; see docs/WORKFLOW.md")
	}
	if len(args) == 0 {
		return bad()
	}
	switch args[0] {
	case "workspace":
		if len(args) != 2 || args[1] != "status" {
			return bad()
		}
		method = "workspace.status"
	case "files":
		method = "files.list"
		rest = args[1:]
	case "read", "symbol", "references", "calls":
		if len(args) < 2 {
			return bad()
		}
		rest = args[2:]
		switch args[0] {
		case "read":
			method = "file.read"
			p.Path = args[1]
		case "symbol":
			method = "symbol.get"
			p.Symbol = args[1]
		case "references":
			method = "references.find"
			p.Symbol = args[1]
		case "calls":
			method = "calls.list"
			p.Symbol = args[1]
		}
	case "tests":
		if len(args) < 3 || args[1] != "related" {
			return bad()
		}
		method = "tests.related"
		p.Symbol = args[2]
		rest = args[3:]
	case "search":
		if len(args) < 2 {
			return bad()
		}
		method = "search.entities"
		p.Query = args[1]
		rest = args[2:]
	case "task":
		if len(args) < 2 {
			return bad()
		}
		sub := args[1]
		if sub == "create" {
			method = "task.create"
			rest = args[2:]
			o.mutation = true
		} else {
			if len(args) < 3 {
				return bad()
			}
			p.Task = args[2]
			rest = args[3:]
			switch sub {
			case "status":
				method = "task.get"
			case "plan", "revise", "cancel", "resume", "export":
				method = "task." + sub
				o.mutation = true
			case "diff":
				method = "task.diff"
			case "approve", "review", "requirements":
				method = "human." + sub
				o.mutation = true
				o.human = true
			default:
				return bad()
			}
		}
	case "patch":
		if len(args) < 3 || args[1] != "apply" {
			return bad()
		}
		method = "patch.apply"
		p.Task = args[2]
		rest = args[3:]
		o.mutation = true
	default:
		return bad()
	}
	f := flag.NewFlagSet(method, flag.ContinueOnError)
	generation := f.String("generation", "", "explicit immutable generation (otherwise live/current)")
	snapshot := f.String("snapshot", "", "explicit source/candidate snapshot")
	limit := f.Int("limit", 0, "page size")
	offset := f.Int("offset", 0, "byte/page offset")
	cursor := f.String("cursor", "", "text search cursor")
	prefix := f.String("prefix", "", "file prefix")
	text := f.Bool("text", false, "literal source text search")
	spec := f.String("spec", "", "JSON/YAML ChangeSpec file")
	plan := f.String("plan", "", "JSON/YAML plan file")
	patch := f.String("patch", "", "JSON/YAML patch file")
	operation := f.String("operation-id", "", "idempotency key")
	revision := f.Int("revision", -1, "expected task execution revision")
	epoch := f.Int("epoch", -1, "expected project epoch")
	actor := f.String("actor", "", "human approver identity label")
	digest := f.String("behavior-digest", "", "exact approved behavior digest")
	output := f.String("output", "", "new output path (never overwrite)")
	format := f.String("format", "patch", "patch or json")
	incomplete := f.Bool("allow-incomplete", false, "explicit diagnostic export without check/reviewer readiness")
	if e := f.Parse(rest); e != nil {
		return "", p, o, e
	}
	if len(f.Args()) > 0 {
		return bad()
	}
	allowed := map[string]bool{}
	addFlags := func(names ...string) {
		for _, n := range names {
			allowed[n] = true
		}
	}
	switch method {
	case "files.list":
		addFlags("generation", "snapshot", "prefix", "offset", "limit")
	case "file.read":
		addFlags("generation", "snapshot", "offset", "limit")
	case "symbol.get", "references.find", "calls.list", "tests.related":
		addFlags("generation", "offset", "limit")
	case "search.entities":
		addFlags("generation", "limit", "text", "cursor")
	}
	if o.mutation {
		addFlags("operation-id", "revision", "epoch")
	}
	switch method {
	case "task.create", "task.revise":
		addFlags("spec")
	case "task.plan":
		addFlags("plan")
	case "patch.apply":
		addFlags("patch")
	case "human.approve", "human.review", "human.requirements":
		addFlags("actor", "behavior-digest")
	case "task.export":
		addFlags("output", "format", "allow-incomplete")
	}
	var invalid string
	f.Visit(func(option *flag.Flag) {
		if !allowed[option.Name] {
			invalid = option.Name
		}
		if option.Name == "revision" && *revision < 0 || option.Name == "epoch" && *epoch < 0 {
			invalid = option.Name
		}
	})
	if invalid != "" {
		return "", p, o, fmt.Errorf("flag --%s is invalid for %s", invalid, method)
	}
	if *cursor != "" && !*text {
		return "", p, o, fmt.Errorf("--cursor requires --text")
	}

	p.Generation = *generation
	p.Snapshot = *snapshot
	p.Limit = *limit
	p.Offset = *offset
	p.Cursor = *cursor
	if method == "files.list" {
		p.Path = *prefix
	}
	if *text {
		if method != "search.entities" {
			return bad()
		}
		method = "search.text"
	}
	p.Operation = *operation
	if *revision >= 0 {
		p.Revision = revision
	}
	if *epoch >= 0 {
		p.Epoch = epoch
	}
	p.Actor = *actor
	p.Digest = *digest
	p.AllowIncomplete = *incomplete
	o.output = *output
	o.format = *format
	load := func(path string, target any) error {
		if path == "" {
			return fmt.Errorf("contract input path is required")
		}
		file, e := os.Open(path)
		if e != nil {
			return e
		}
		defer file.Close()
		b, e := io.ReadAll(io.LimitReader(file, 524289))
		if e != nil {
			return e
		}
		return contracts.Decode(b, target)
	}
	switch method {
	case "task.create", "task.revise":
		p.Spec = &domain.ChangeSpec{}
		if e := load(*spec, p.Spec); e != nil {
			return "", p, o, e
		}
	case "task.plan":
		p.Plan = &domain.Plan{}
		if e := load(*plan, p.Plan); e != nil {
			return "", p, o, e
		}
	case "patch.apply":
		p.Patch = &domain.Patch{}
		if e := load(*patch, p.Patch); e != nil {
			return "", p, o, e
		}
	case "task.export":
		if *output == "" || (*format != "patch" && *format != "json") {
			return "", p, o, fmt.Errorf("export requires --output and --format patch|json")
		}
	}
	if method != "task.export" && *output != "" {
		return bad()
	}
	return method, p, o, nil
}
func approve(ctx context.Context, socket string, p application.Params, method string) int {
	if p.Actor == "" {
		fmt.Fprintln(os.Stderr, "--actor required; approval must be performed by the human, not the model")
		return 2
	}
	env, e := transports.Call(ctx, socket, "task.get", application.Params{Task: p.Task})
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 4
	}
	if env.Error != nil {
		return printEnvelope(env)
	}
	raw, _ := json.Marshal(env.Data)
	var t domain.Task
	if json.Unmarshal(raw, &t) != nil {
		return 4
	}
	expected := t.BehaviorDigest
	if method == "human.review" {
		if t.CheckSummary["checks_passed"] != true {
			fmt.Fprintln(os.Stderr, "current product checks must pass first")
			return 3
		}
		expected, _ = t.CheckSummary["review_digest"].(string)
	}
	if p.Digest == "" {
		p.Digest = expected
	}
	if p.Digest != expected {
		fmt.Fprintln(os.Stderr, "behavior digest does not match task")
		return 3
	}
	tty, e := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if e != nil {
		fmt.Fprintln(os.Stderr, "interactive human terminal required; approval cannot be piped or supplied by a skill")
		return 3
	}
	defer tty.Close()
	fmt.Fprintf(tty, "Approve task %q\nObjective: %q\n", t.Alias, t.Spec.Objective)
	for _, c := range t.Spec.Criteria {
		fmt.Fprintf(tty, "%q: %q\n", c.ID, c.Expected)
	}
	challenge := method + " " + t.Alias + " " + expected
	fmt.Fprintf(tty, "Candidate: %s\nDecision digest: %s\nType exactly: %s\n> ", t.Candidate, expected, challenge)
	line, e := bufio.NewReader(tty).ReadString('\n')
	if e != nil || strings.TrimSpace(line) != challenge {
		fmt.Fprintln(os.Stderr, "approval declined")
		return 3
	}
	env, e = transports.HumanDecision(ctx, socket, method, p)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 4
	}
	return printEnvelope(env)
}
func writeExport(o extendedOptions, env transports.Envelope) error {
	data, e := json.MarshalIndent(env.Data, "", "  ")
	if e != nil {
		return e
	}
	var value struct {
		Patch string `json:"patch"`
	}
	if e = json.Unmarshal(data, &value); e != nil {
		return e
	}
	if o.format == "json" {
		return newFile(o.output, append(data, '\n'))
	}
	// Manifest keeps all before/after hashes and the explicit NOT_REVIEW_READY label.
	sidecar := o.output + ".manifest.json"
	if _, e = os.Lstat(sidecar); e == nil {
		return fmt.Errorf("output already exists: %s", sidecar)
	} else if !os.IsNotExist(e) {
		return e
	}
	if e = newFile(o.output, []byte(value.Patch)); e != nil {
		return e
	}
	if e = newFile(sidecar, append(data, '\n')); e != nil {
		os.Remove(o.output)
		return e
	}
	return nil
}
func newFile(path string, b []byte) error {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	if _, e = f.Write(b); e != nil {
		f.Close()
		os.Remove(path)
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		os.Remove(path)
		return e
	}
	return f.Close()
}
