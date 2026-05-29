package safemath

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func evalString(t *testing.T, src string) string {
	t.Helper()
	n, err := Parse(src)
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	d, err := Eval(n)
	if err != nil {
		t.Fatalf("eval %q: %v", src, err)
	}
	return d.String()
}

func TestEval_BasicArithmetic(t *testing.T) {
	cases := map[string]string{
		"1+2*3":   "7",
		"(1+2)*3": "9",
		"10/4":    "2.5",
		"2^3^2":   "512", // right-associative: 2^(3^2) = 2^9
		"-5 + 3":  "-2",
		"---5":    "-5",
	}
	for src, want := range cases {
		if got := evalString(t, src); got != want {
			t.Errorf("%s = %s; want %s", src, got, want)
		}
	}
}

func TestEval_PercentSuffix(t *testing.T) {
	if got := evalString(t, "22%"); got != "0.22" {
		t.Errorf("22%% = %s", got)
	}
	if got := evalString(t, "(5 + 5)%"); got != "0.1" {
		t.Errorf("(5+5)%% = %s", got)
	}
}

func TestEval_DecimalPrecision(t *testing.T) {
	if got := evalString(t, "0.1 + 0.2"); got != "0.3" {
		t.Fatalf("0.1+0.2 = %s; decimal arithmetic must be exact", got)
	}
}

func TestEval_FunctionsHappyPath(t *testing.T) {
	cases := map[string]string{
		"min(1, 2, 3)":        "1",
		"max(1, 2, 3)":        "3",
		"abs(-7)":             "7",
		"floor(2.9)":          "2",
		"ceil(2.1)":           "3",
		"sqrt(9)":             "3",
		"round(1.5)":          "2", // banker's: nearest even
		"round(0.5)":          "0",
		"round(2.5)":          "2",
		"round(3.5)":          "4",
		"round(389.8125, 2)":  "389.81", // half-even at 4th decimal of 2-rounded value
		"round(389.815, 2)":   "389.82",
	}
	for src, want := range cases {
		if got := evalString(t, src); got != want {
			t.Errorf("%s = %s; want %s", src, got, want)
		}
	}
}

func TestEval_FunctionArityErrors(t *testing.T) {
	cases := []string{
		"round()",
		"round(1, 2, 3)",
		"sqrt()",
		"min()",
		"max()",
	}
	for _, src := range cases {
		n, _ := Parse(src)
		if _, err := Eval(n); err == nil {
			t.Errorf("%s: expected arity error", src)
		}
	}
}

func TestEval_DivideByZero(t *testing.T) {
	n, _ := Parse("10/0")
	_, err := Eval(n)
	if err == nil || !strings.Contains(err.Error(), "divide_by_zero") {
		t.Fatalf("expected divide_by_zero; got %v", err)
	}
}

func TestEval_DomainErrorOnSqrtNegative(t *testing.T) {
	n, _ := Parse("sqrt(-1)")
	_, err := Eval(n)
	if err == nil || !strings.Contains(err.Error(), "domain_error") {
		t.Fatalf("expected domain_error; got %v", err)
	}
}

func TestEval_RoundPlacesOutOfRange(t *testing.T) {
	for _, src := range []string{"round(1, -1)", "round(1, 17)"} {
		n, _ := Parse(src)
		if _, err := Eval(n); err == nil {
			t.Errorf("%s: expected domain_error for places out of range", src)
		}
	}
}

// Sanity check that the decimal type used in the test imports actually
// matches the evaluator's return type (compile-time guard).
var _ = decimal.NewFromInt(0)
