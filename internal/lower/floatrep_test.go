package lower

import "testing"

// intArith keeps an int result only where integer and double arithmetic agree
// bit for bit; anything else must make the set top so the value stays boxed.
func TestIntArithAgreesWithDoubles(t *testing.T) {
	set := func(vs ...int64) intSet {
		var s intSet
		for _, v := range vs {
			s = s.add(v)
		}
		return s
	}
	cases := []struct {
		name string
		op   string
		a, b intSet
		want intSet
	}{
		{"zero times zero", "*", set(0), set(0), set(0)},
		// int 0, but 0.0 * -3.0 is -0.0.
		{"signed zero", "*", set(0), set(-3), intSet{top: true}},
		{"sum", "+", set(1, 2), set(10), set(11, 12)},
		// A float on either side converts the other.
		{"float operand", "+", set(1), intSet{}, intSet{}},
		{"unknown int", "+", intSet{top: true}, set(1), intSet{top: true}},
		// 8/2 is int 4; 3/2 is float 1.5, which is no int at all.
		{"exact quotient", "/", set(8), set(2), set(4)},
		{"inexact quotient", "/", set(3), set(2), intSet{}},
		{"zero divisor throws", "/", set(1), set(0), intSet{}},
		// 0 / -5 is int 0, but 0.0 / -5.0 is -0.0.
		{"quotient signed zero", "/", set(0), set(-5), intSet{top: true}},
		{"past the exact range", "*", set(1 << 30), set(1 << 30), intSet{top: true}},
	}
	for _, c := range cases {
		if got := intArith(c.op, c.a, c.b); !got.equal(c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}
