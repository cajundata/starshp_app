package safemath

type TokenKind int

const (
	TEOF TokenKind = iota
	TInt
	TFloat
	TIdent
	TPlus
	TMinus
	TStar
	TSlash
	TCaret
	TLParen
	TRParen
	TComma
	TPercent
)

type Token struct {
	Kind TokenKind
	Text string
	Pos  int // 0-based byte offset of the token start
}
