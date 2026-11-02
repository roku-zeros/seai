package main

import (
	"encoding/json"
	"io"
	"os"
	business "seai.fixture/business"
)

func main() {
	var in business.Input
	d := json.NewDecoder(os.Stdin)
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil {
		os.Exit(2)
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		os.Exit(2)
	}
	if json.NewEncoder(os.Stdout).Encode(business.Evaluate(in)) != nil {
		os.Exit(2)
	}
}
