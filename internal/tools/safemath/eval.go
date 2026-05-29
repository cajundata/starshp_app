package safemath

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

var (
	decZero    = decimal.Zero
	decHundred = decimal.NewFromInt(100)

	ErrDivideByZero = errors.New("divide_by_zero: division by zero")
)

// Eval walks the AST and returns a Decimal. Errors are tagged with a
// stable prefix (parse_error, divide_by_zero, domain_error) so the tool
// wrapper can map them to normalized error codes.
func Eval(n Node) (decimal.Decimal, error) {
	switch x := n.(type) {
	case *NumLit:
		d, err := decimal.NewFromString(x.Text)
		if err != nil {
			return decZero, fmt.Errorf("parse_error: invalid number %q", x.Text)
		}
		return d, nil
	case *Unary:
		v, err := Eval(x.Expr)
		if err != nil {
			return decZero, err
		}
		if x.Op == "-" {
			return v.Neg(), nil
		}
		return v, nil
	case *Postfix:
		v, err := Eval(x.Expr)
		if err != nil {
			return decZero, err
		}
		if x.Op == "%" {
			return v.Div(decHundred), nil
		}
		return decZero, fmt.Errorf("parse_error: unknown postfix %q", x.Op)
	case *BinaryOp:
		l, err := Eval(x.Left)
		if err != nil {
			return decZero, err
		}
		r, err := Eval(x.Right)
		if err != nil {
			return decZero, err
		}
		return applyBinary(x.Op, l, r)
	case *FuncCall:
		return applyFunc(x)
	}
	return decZero, fmt.Errorf("parse_error: unknown node type %T", n)
}

func applyBinary(op string, l, r decimal.Decimal) (decimal.Decimal, error) {
	switch op {
	case "+":
		return l.Add(r), nil
	case "-":
		return l.Sub(r), nil
	case "*":
		return l.Mul(r), nil
	case "/":
		if r.IsZero() {
			return decZero, ErrDivideByZero
		}
		return l.Div(r), nil
	case "^":
		// shopspring/decimal Pow uses integer exponents reliably; for
		// fractional exponents fall back to Pow which uses an internal series.
		return l.Pow(r), nil
	}
	return decZero, fmt.Errorf("parse_error: unknown operator %q", op)
}

func applyFunc(f *FuncCall) (decimal.Decimal, error) {
	args := make([]decimal.Decimal, len(f.Args))
	for i, a := range f.Args {
		v, err := Eval(a)
		if err != nil {
			return decZero, err
		}
		args[i] = v
	}
	name := f.Name
	arityErr := func(want string) error {
		return fmt.Errorf("parse_error: %s: wrong number of arguments (got %d, want %s)", name, len(args), want)
	}
	switch name {
	case "min":
		if len(args) == 0 {
			return decZero, arityErr(">=1")
		}
		m := args[0]
		for _, a := range args[1:] {
			if a.LessThan(m) {
				m = a
			}
		}
		return m, nil
	case "max":
		if len(args) == 0 {
			return decZero, arityErr(">=1")
		}
		m := args[0]
		for _, a := range args[1:] {
			if a.GreaterThan(m) {
				m = a
			}
		}
		return m, nil
	case "abs":
		if len(args) != 1 {
			return decZero, arityErr("1")
		}
		return args[0].Abs(), nil
	case "floor":
		if len(args) != 1 {
			return decZero, arityErr("1")
		}
		return args[0].Floor(), nil
	case "ceil":
		if len(args) != 1 {
			return decZero, arityErr("1")
		}
		return args[0].Ceil(), nil
	case "sqrt":
		if len(args) != 1 {
			return decZero, arityErr("1")
		}
		if args[0].IsNegative() {
			return decZero, fmt.Errorf("domain_error: sqrt of negative")
		}
		// shopspring/decimal exposes Sqrt via Pow(0.5) with explicit precision.
		// Use high precision for the use case (16) and let the decimal display
		// handle truncation.
		return args[0].Pow(decimal.NewFromFloat(0.5)).Round(16), nil
	case "round":
		switch len(args) {
		case 1:
			return args[0].RoundBank(0), nil
		case 2:
			places := args[1]
			if !places.IsInteger() {
				return decZero, fmt.Errorf("domain_error: round places must be an integer")
			}
			p := places.IntPart()
			if p < 0 || p > 16 {
				return decZero, fmt.Errorf("domain_error: round places must be in [0, 16]; got %d", p)
			}
			return args[0].RoundBank(int32(p)), nil
		}
		return decZero, arityErr("1 or 2")
	}
	return decZero, fmt.Errorf("parse_error: unknown function %q", name)
}
