package safemath

import (
	"fmt"
	"unicode"
	"unicode/utf8"
)

const maxExpressionLen = 1000

func lex(src string) ([]Token, error) {
	if len(src) > maxExpressionLen {
		return nil, fmt.Errorf("expression too long: %d chars (max %d)", len(src), maxExpressionLen)
	}
	var toks []Token
	i := 0
	for i < len(src) {
		r, w := utf8.DecodeRuneInString(src[i:])
		if unicode.IsSpace(r) {
			i += w
			continue
		}
		start := i
		switch {
		case unicode.IsDigit(r):
			tk, n := scanNumber(src, i)
			toks = append(toks, Token{Kind: tk.Kind, Text: tk.Text, Pos: start})
			i += n
		case unicode.IsLetter(r) || r == '_':
			tk, n := scanIdent(src, i)
			toks = append(toks, Token{Kind: tk.Kind, Text: tk.Text, Pos: start})
			i += n
		case r == '+':
			toks = append(toks, Token{Kind: TPlus, Text: "+", Pos: start})
			i++
		case r == '-':
			toks = append(toks, Token{Kind: TMinus, Text: "-", Pos: start})
			i++
		case r == '*':
			toks = append(toks, Token{Kind: TStar, Text: "*", Pos: start})
			i++
		case r == '/':
			toks = append(toks, Token{Kind: TSlash, Text: "/", Pos: start})
			i++
		case r == '^':
			toks = append(toks, Token{Kind: TCaret, Text: "^", Pos: start})
			i++
		case r == '(':
			toks = append(toks, Token{Kind: TLParen, Text: "(", Pos: start})
			i++
		case r == ')':
			toks = append(toks, Token{Kind: TRParen, Text: ")", Pos: start})
			i++
		case r == ',':
			toks = append(toks, Token{Kind: TComma, Text: ",", Pos: start})
			i++
		case r == '%':
			toks = append(toks, Token{Kind: TPercent, Text: "%", Pos: start})
			i++
		default:
			return nil, fmt.Errorf("unexpected character %q at position %d", r, start)
		}
	}
	toks = append(toks, Token{Kind: TEOF, Pos: i})
	return toks, nil
}

func scanNumber(src string, start int) (Token, int) {
	i := start
	seenDot := false
	for i < len(src) {
		r, w := utf8.DecodeRuneInString(src[i:])
		if unicode.IsDigit(r) {
			i += w
			continue
		}
		if r == '.' && !seenDot {
			seenDot = true
			i += w
			continue
		}
		break
	}
	text := src[start:i]
	if seenDot {
		return Token{Kind: TFloat, Text: text}, i - start
	}
	return Token{Kind: TInt, Text: text}, i - start
}

func scanIdent(src string, start int) (Token, int) {
	i := start
	for i < len(src) {
		r, w := utf8.DecodeRuneInString(src[i:])
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			i += w
			continue
		}
		break
	}
	return Token{Kind: TIdent, Text: src[start:i]}, i - start
}
