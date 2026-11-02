// Package business is a synthetic business-rules project for SEAI acceptance tests.
package business

import "sort"

type Input struct {
	Age     int    `json:"age"`
	Income  int    `json:"income"`
	VIP     bool   `json:"vip"`
	Product string `json:"product"`
}
type Decision struct {
	Allowed bool     `json:"allowed"`
	Reason  string   `json:"reason"`
	Limit   int      `json:"limit"`
	Checked []string `json:"checked_rules"`
	Applied []string `json:"applied_rules"`
}
type Rule struct {
	ID       string
	Product  string
	Priority int
	When     func(Input) bool
	Except   func(Input) bool
	Apply    func(Input, *Decision)
}

const AgeRule = "AGE_MIN"
const IncomeRule = "INCOME_MIN"
const LimitRule = "CREDIT_LIMIT"
const MinimumAge = 18

func AgeThreshold() int          { return MinimumAge }
func TooYoung(in Input) bool     { return in.Age < AgeThreshold() }
func LowIncome(in Input) bool    { return in.Income < 2000 }
func VIPException(in Input) bool { return in.VIP }
func Never(Input) bool           { return false }
func Always(Input) bool          { return true }
func RejectAge(_ Input, out *Decision) {
	if out.Allowed {
		out.Allowed = false
		out.Reason = "below_minimum_age"
	}
}
func RejectIncome(_ Input, out *Decision) {
	if out.Allowed {
		out.Allowed = false
		out.Reason = "below_minimum_income"
	}
}
func AssignLimit(in Input, out *Decision) {
	if out.Allowed {
		out.Limit = in.Income * 2
	}
}

var Rules = []Rule{
	{ID: AgeRule, Product: "retail", Priority: 10, When: TooYoung, Except: VIPException, Apply: RejectAge},
	{ID: IncomeRule, Product: "retail", Priority: 20, When: LowIncome, Except: Never, Apply: RejectIncome},
	{ID: LimitRule, Product: "retail", Priority: 30, When: Always, Except: Never, Apply: AssignLimit},
	{ID: AgeRule, Product: "business", Priority: 10, When: TooYoung, Except: Never, Apply: RejectAge},
}

func Evaluate(in Input) Decision {
	out := Decision{Allowed: true, Reason: "eligible", Checked: []string{}, Applied: []string{}}
	rules := append([]Rule{}, Rules...)
	sort.SliceStable(rules, func(i, j int) bool { return rules[i].Priority < rules[j].Priority })
	for _, rule := range rules {
		if rule.Product != in.Product {
			continue
		}
		out.Checked = append(out.Checked, rule.ID)
		if rule.When(in) && !rule.Except(in) {
			rule.Apply(in, &out)
			out.Applied = append(out.Applied, rule.ID)
		}
	}
	return out
}
