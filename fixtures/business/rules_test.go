package business

import "testing"

func TestBusinessBoundaries(t *testing.T) {
	for _, c := range []struct {
		age     int
		allowed bool
	}{{17, false}, {18, true}, {19, true}} {
		got := Evaluate(Input{Age: c.age, Income: 3000, Product: "retail"})
		if got.Allowed != c.allowed {
			t.Fatalf("age=%d %+v", c.age, got)
		}
	}
}
func TestBusinessExceptionAndPriority(t *testing.T) {
	vip := Evaluate(Input{Age: 17, Income: 3000, VIP: true, Product: "retail"})
	if !vip.Allowed || vip.Limit != 6000 {
		t.Fatal(vip)
	}
	combined := Evaluate(Input{Age: 17, Income: 1000, Product: "retail"})
	if combined.Reason != "below_minimum_age" || combined.Limit != 0 {
		t.Fatal(combined)
	}
	other := Evaluate(Input{Age: 17, Income: 3000, VIP: true, Product: "business"})
	if other.Allowed {
		t.Fatal(other)
	}
}
