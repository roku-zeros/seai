// Synthetic executable fixture for SEAI baseline/candidate comparisons.
package main

import (
	"encoding/json"
	"os"
	strategy "seai.fixture/strategy"
)

func main() {
	var input struct {
		Age int `json:"age"`
	}
	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil {
		os.Exit(2)
	}
	a := strategy.Applicant{Age: input.Age}
	if json.NewEncoder(os.Stdout).Encode(map[string]any{"eligible": strategy.Eligible(a), "reason": strategy.EligibleReason(a)}) != nil {
		os.Exit(2)
	}
}
