package strategy

import "testing"

func TestEligibleBoundary(t *testing.T) {
	for _, tc := range []struct {
		age  int
		want bool
	}{{17, false}, {18, true}, {19, true}} {
		if got := Eligible(Applicant{Age: tc.age}); got != tc.want {
			t.Fatalf("age=%d got=%v want=%v", tc.age, got, tc.want)
		}
	}
}
