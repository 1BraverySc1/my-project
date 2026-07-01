package tests

import (
	"testing"

	"webdownld_go/internal/payment"
)

func TestAmountToCent(t *testing.T) {
	tests := map[string]int64{
		"0":      0,
		"19.9":   1990,
		"159.90": 15990,
	}
	for input, want := range tests {
		got, err := payment.AmountToCent(input)
		if err != nil || got != want {
			t.Fatalf("AmountToCent(%q)=(%d,%v), want (%d,nil)", input, got, err, want)
		}
	}
	for _, input := range []string{"", "-1.00", "1.001", "abc"} {
		if _, err := payment.AmountToCent(input); err == nil {
			t.Fatalf("AmountToCent(%q) should fail", input)
		}
	}
}
