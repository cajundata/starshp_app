package safemath

import "fmt"

const maxParseDepth = 50

type Node interface{ node() }

type NumLit struct{ Text string }
type BinaryOp struct {
	Op          string
	Left, Right Node
}
type Unary struct {
	Op   string
	Expr Node
}
type Postfix struct {
	Op   string
	Expr Node
}
type FuncCall struct {
	Name string
	Args []Node
}

func (*NumLit) node()   {}
func (*BinaryOp) node() {}
func (*Unary) node()    {}
func (*Postfix) node()  {}
func (*FuncCall) node() {}

type ParseError struct {
	Msg string
	Pos int
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("parse error at position %d: %s", e.Pos, e.Msg)
}

type parser struct {
	toks  []Token
	i     int
	depth int
}

// Parse converts an expression into an AST. The returned error is always
// *ParseError on syntactic failure (so callers can surface position info)
// or a generic error for limits.
func Parse(src string) (Node, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	n, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.peek().Kind != TEOF {
		return nil, &ParseError{Msg: fmt.Sprintf("unexpected token %q", p.peek().Text), Pos: p.peek().Pos}
	}
	return n, nil
}

func (p *parser) peek() Token { return p.toks[p.i] }
func (p *parser) advance() Token {
	t := p.toks[p.i]
	p.i++
	return t
}

func (p *parser) enter() error {
	p.depth++
	if p.depth > maxParseDepth {
		return fmt.Errorf("parse depth exceeded (max %d)", maxParseDepth)
	}
	return nil
}
func (p *parser) leave() { p.depth-- }

// expr := term (('+' | '-') term)*
func (p *parser) parseExpr() (Node, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for {
		switch p.peek().Kind {
		case TPlus, TMinus:
			op := p.advance().Text
			right, err := p.parseTerm()
			if err != nil {
				return nil, err
			}
			left = &BinaryOp{Op: op, Left: left, Right: right}
		default:
			return left, nil
		}
	}
}

// term := factor (('*' | '/') factor)*
func (p *parser) parseTerm() (Node, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	left, err := p.parseFactor()
	if err != nil {
		return nil, err
	}
	for {
		switch p.peek().Kind {
		case TStar, TSlash:
			op := p.advance().Text
			right, err := p.parseFactor()
			if err != nil {
				return nil, err
			}
			left = &BinaryOp{Op: op, Left: left, Right: right}
		default:
			return left, nil
		}
	}
}

// factor := unary ('^' factor)?    -- right-associative
func (p *parser) parseFactor() (Node, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	if p.peek().Kind == TCaret {
		p.advance()
		right, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		return &BinaryOp{Op: "^", Left: left, Right: right}, nil
	}
	return left, nil
}

// unary := ('-' | '+') unary | postfix
func (p *parser) parseUnary() (Node, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	switch p.peek().Kind {
	case TMinus, TPlus:
		op := p.advance().Text
		expr, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &Unary{Op: op, Expr: expr}, nil
	}
	return p.parsePostfix()
}

// postfix := primary '%'?
func (p *parser) parsePostfix() (Node, error) {
	n, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	if p.peek().Kind == TPercent {
		p.advance()
		return &Postfix{Op: "%", Expr: n}, nil
	}
	return n, nil
}

func (p *parser) parsePrimary() (Node, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	tk := p.peek()
	switch tk.Kind {
	case TInt, TFloat:
		p.advance()
		return &NumLit{Text: tk.Text}, nil
	case TLParen:
		p.advance()
		n, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.peek().Kind != TRParen {
			return nil, &ParseError{Msg: "missing ')'", Pos: p.peek().Pos}
		}
		p.advance()
		return n, nil
	case TIdent:
		name := tk.Text
		p.advance()
		if p.peek().Kind != TLParen {
			return nil, &ParseError{Msg: fmt.Sprintf("unknown identifier %q (no constants defined)", name), Pos: tk.Pos}
		}
		p.advance() // '('
		var args []Node
		if p.peek().Kind != TRParen {
			for {
				arg, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				args = append(args, arg)
				if p.peek().Kind == TComma {
					p.advance()
					continue
				}
				break
			}
		}
		if p.peek().Kind != TRParen {
			return nil, &ParseError{Msg: "missing ')' in call", Pos: p.peek().Pos}
		}
		p.advance()
		return &FuncCall{Name: name, Args: args}, nil
	}
	return nil, &ParseError{Msg: fmt.Sprintf("unexpected token %q", tk.Text), Pos: tk.Pos}
}
