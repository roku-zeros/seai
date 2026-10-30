// Package strategy is synthetic test input, not an approved business rule.
package strategy

const MinimumAge = 18

type Applicant struct{ Age int }

func Eligible(a Applicant) bool { return a.Age >= MinimumAge }

func EligibleReason(a Applicant) string {
	if Eligible(a) {
		return "eligible"
	}
	return "below minimum age"
}
