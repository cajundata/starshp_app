package safemath

import "testing"

func TestParser_RespectsPrecedence(t *testing.T) {
	n, err := Parse("1 + 2 * 3")
	if err != nil {
		t.Fatal(err)
	}
	add, ok := n.(*BinaryOp)
	if !ok || add.Op != "+" {
		t.Fatalf("root must be +; got %T", n)
	}
	mul, ok := add.Right.(*BinaryOp)
	if !ok || mul.Op != "*" {
		t.Fatalf("right must be *; got %T", add.Right)
	}
}

func TestParser_PowerRightAssociative(t *testing.T) {
	n, err := Parse("2 ^ 3 ^ 2")
	if err != nil {
		t.Fatal(err)
	}
	pow, _ := n.(*BinaryOp)
	if pow.Op != "^" {
		t.Fatalf("root must be ^; got %s", pow.Op)
	}
	right, _ := pow.Right.(*BinaryOp)
	if right == nil || right.Op != "^" {
		t.Fatalf("right operand of ^ must be ^ (right-assoc); got %T", pow.Right)
	}
}

func TestParser_UnaryStacking(t *testing.T) {
	n, err := Parse("---5")
	if err != nil {
		t.Fatal(err)
	}
	u1, _ := n.(*Unary)
	u2, _ := u1.Expr.(*Unary)
	u3, _ := u2.Expr.(*Unary)
	if u1 == nil || u2 == nil || u3 == nil {
		t.Fatalf("expected three nested unary minus; got %T", n)
	}
}

func TestParser_FunctionWithVarArgs(t *testing.T) {
	n, err := Parse("max(1, 2, 3, 4)")
	if err != nil {
		t.Fatal(err)
	}
	call, _ := n.(*FuncCall)
	if call == nil || call.Name != "max" || len(call.Args) != 4 {
		t.Fatalf("max call mismatch: %+v", n)
	}
}

func TestParser_PercentSuffixOnLiteralAndGroup(t *testing.T) {
	if _, err := Parse("22%"); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse("(5 + 5)%"); err != nil {
		t.Fatal(err)
	}
}

func TestParser_DepthExceeded(t *testing.T) {
	s := ""
	for i := 0; i < 60; i++ {
		s += "("
	}
	s += "1"
	for i := 0; i < 60; i++ {
		s += ")"
	}
	if _, err := Parse(s); err == nil {
		t.Fatal("expected depth_exceeded")
	}
}

func TestParser_ReportsErrorLocation(t *testing.T) {
	_, err := Parse("1 + ")
	if err == nil {
		t.Fatal("expected parse error")
	}
	if perr, ok := err.(*ParseError); !ok || perr.Pos == 0 {
		t.Fatalf("expected ParseError with non-zero Pos; got %v", err)
	}
}
