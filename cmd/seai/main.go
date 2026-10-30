package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"seai.local/seai/internal/application"
	"seai.local/seai/internal/transports"
	"time"
)

func main() { os.Exit(run()) }
func run() int {
	f := flag.NewFlagSet("seai", flag.ContinueOnError)
	data := f.String("data", "", "daemon runtime directory")
	asJSON := f.Bool("json", false, "JSON output (also default)")
	_ = asJSON
	if e := f.Parse(os.Args[1:]); e != nil {
		return 2
	}
	args := f.Args()
	if *data == "" || len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: seai --data <runtime> [--json] doctor|project status|index build [--ref REF] [--wait]|job status ID|search QUERY")
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	socket := filepath.Join(*data, "seaid.sock")
	hand, e := transports.Call(ctx, socket, "handshake", application.Params{})
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 4
	}
	if hand.Error != nil {
		return printEnvelope(hand)
	}
	p := application.Params{}
	method := ""
	wait := false
	switch args[0] {
	case "doctor":
		if len(args) != 1 {
			return 2
		}
		method = "capabilities.get"
	case "project":
		if len(args) != 2 || args[1] != "status" {
			return 2
		}
		method = "project.get"
	case "index":
		if len(args) < 2 || args[1] != "build" {
			return 2
		}
		sub := flag.NewFlagSet("index build", flag.ContinueOnError)
		ref := sub.String("ref", "", "explicit immutable Git ref (default working tree)")
		w := sub.Bool("wait", false, "wait for terminal job")
		op := sub.String("operation-id", "", "idempotency key (default random)")
		epoch := sub.Int("epoch", -1, "expected project epoch (default read current)")
		if sub.Parse(args[2:]) != nil || len(sub.Args()) > 0 {
			return 2
		}
		wait = *w
		p.Ref = *ref
		p.Operation = *op
		if p.Operation == "" {
			b := make([]byte, 16)
			if _, e = rand.Read(b); e != nil {
				fmt.Fprintln(os.Stderr, e)
				return 4
			}
			p.Operation = hex.EncodeToString(b)
		}
		if *epoch < 0 {
			env, e := transports.Call(ctx, socket, "project.get", p)
			if e != nil {
				fmt.Fprintln(os.Stderr, e)
				return 4
			}
			if env.Error != nil {
				return printEnvelope(env)
			}
			b, _ := json.Marshal(env.Data)
			var project struct {
				Epoch int `json:"epoch"`
			}
			if json.Unmarshal(b, &project) != nil {
				return 4
			}
			*epoch = project.Epoch
		}
		p.Epoch = epoch
		method = "index.build"
	case "job":
		if len(args) != 3 || args[1] != "status" {
			return 2
		}
		method = "jobs.get"
		p.Job = args[2]
	case "search":
		if len(args) != 2 {
			return 2
		}
		method = "search.entities"
		p.Query = args[1]
	default:
		fmt.Fprintln(os.Stderr, "unsupported command:", args[0])
		return 2
	}
	env, e := transports.Call(ctx, socket, method, p)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 4
	}
	if method == "index.build" && wait && env.Error == nil {
		for {
			env, e = transports.Call(ctx, socket, "jobs.get", application.Params{Job: p.Operation})
			if e != nil {
				fmt.Fprintln(os.Stderr, e)
				return 4
			}
			if env.Error != nil {
				break
			}
			b, _ := json.Marshal(env.Data)
			var job application.Job
			if json.Unmarshal(b, &job) != nil {
				return 4
			}
			if job.State == "SUCCEEDED" {
				break
			}
			if job.State == "FAILED" || job.State == "ABORTED" {
				printEnvelope(env)
				return 4
			}
			select {
			case <-ctx.Done():
				fmt.Fprintln(os.Stderr, "waiting cancelled; server job continues:", p.Operation)
				return 4
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
	return printEnvelope(env)
}
func printEnvelope(env transports.Envelope) int {
	b, _ := json.MarshalIndent(env, "", "  ")
	fmt.Println(string(b))
	if env.Error == nil {
		return 0
	}
	switch env.Error.Code {
	case "SCHEMA_INVALID", "INVALID_REF", "INVALID_PATH":
		return 2
	case "REVISION_CONFLICT", "IDEMPOTENCY_CONFLICT", "WORKFLOW_BUSY", "NOT_READY":
		return 3
	}
	return 4
}
