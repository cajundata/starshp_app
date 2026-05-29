package safemath

import (
	"reflect"
	"testing"
)

func TestLexer_NumbersAndOperators(t *testing.T) {
	toks, err := lex("12 + 3.5 * (2 - 1)^2")
	if err != nil {
		t.Fatal(err)
	}
	kinds := tokenKinds(toks)
	want := []TokenKind{TInt, TPlus, TFloat, TStar, TLParen, TInt, TMinus, TInt, TRParen, TCaret, TInt, TEOF}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("got %v\nwant %v", kinds, want)
	}
}

func TestLexer_PercentSuffix(t *testing.T) {
	toks, _ := lex("22%")
	kinds := tokenKinds(toks)
	if !reflect.DeepEqual(kinds, []TokenKind{TInt, TPercent, TEOF}) {
		t.Fatalf("got %v", kinds)
	}
}

func TestLexer_FunctionCall(t *testing.T) {
	toks, _ := lex("round(1.5, 0)")
	kinds := tokenKinds(toks)
	if !reflect.DeepEqual(kinds, []TokenKind{TIdent, TLParen, TFloat, TComma, TInt, TRParen, TEOF}) {
		t.Fatalf("got %v", kinds)
	}
}

func TestLexer_UnknownCharacter(t *testing.T) {
	if _, err := lex("1 ? 2"); err == nil {
		t.Fatal("expected lexer error on unknown character")
	}
}

func tokenKinds(toks []Token) []TokenKind {
	out := make([]TokenKind, len(toks))
	for i, tk := range toks {
		out[i] = tk.Kind
	}
	return out
}
