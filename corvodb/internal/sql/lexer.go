// Package sql turns SQL text into an abstract syntax tree.
package sql

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/juliocesar04-code/proj-1/corvodb/internal/types"
)

// Kind classifies a token.
type Kind uint8

// Token kinds.
const (
	EOF Kind = iota
	Ident
	Keyword
	Number
	String
	Operator
)

// Token is a lexical unit with the position it came from.
type Token struct {
	Kind Kind
	Text string
	Line int
	Col  int
}

func (t Token) String() string {
	if t.Kind == EOF {
		return "end of input"
	}
	return strconv.Quote(t.Text)
}

var keywords = map[string]bool{}

func init() {
	for _, word := range strings.Fields(`
		AND AS ASC BEGIN BETWEEN BY COMMIT CREATE DELETE DESC DISTINCT DROP
		EXISTS EXPLAIN FALSE FROM GROUP HAVING IF IN INDEX INNER INSERT INTO IS
		JOIN KEY LIMIT LIKE NOT NULL OFFSET ON OR ORDER PRIMARY ROLLBACK SELECT
		SET TABLE TRUE UNIQUE UPDATE VALUES WHERE
	`) {
		keywords[word] = true
	}
}

// SyntaxError carries the position of the offending token.
type SyntaxError struct {
	Message string
	Line    int
	Col     int
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("syntax error at line %d, column %d: %s", e.Line, e.Col, e.Message)
}

type lexer struct {
	input string
	pos   int
	line  int
	col   int
}

func newLexer(input string) *lexer {
	return &lexer{input: input, line: 1, col: 1}
}

func (l *lexer) errorf(line, col int, format string, args ...any) error {
	return &SyntaxError{Message: fmt.Sprintf(format, args...), Line: line, Col: col}
}

func (l *lexer) peekByte(offset int) byte {
	if l.pos+offset >= len(l.input) {
		return 0
	}
	return l.input[l.pos+offset]
}

func (l *lexer) advance() byte {
	c := l.input[l.pos]
	l.pos++
	if c == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return c
}

func (l *lexer) skipSpaceAndComments() error {
	for l.pos < len(l.input) {
		c := l.input[l.pos]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			l.advance()
		case c == '-' && l.peekByte(1) == '-':
			for l.pos < len(l.input) && l.input[l.pos] != '\n' {
				l.advance()
			}
		case c == '/' && l.peekByte(1) == '*':
			line, col := l.line, l.col
			l.advance()
			l.advance()
			for {
				if l.pos >= len(l.input) {
					return l.errorf(line, col, "unterminated block comment")
				}
				if l.input[l.pos] == '*' && l.peekByte(1) == '/' {
					l.advance()
					l.advance()
					break
				}
				l.advance()
			}
		default:
			return nil
		}
	}
	return nil
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

func isIdentPart(c byte) bool { return isIdentStart(c) || isDigit(c) }

// next returns the token starting at the current position.
func (l *lexer) next() (Token, error) {
	if err := l.skipSpaceAndComments(); err != nil {
		return Token{}, err
	}
	line, col := l.line, l.col
	if l.pos >= len(l.input) {
		return Token{Kind: EOF, Line: line, Col: col}, nil
	}

	c := l.input[l.pos]
	switch {
	case isIdentStart(c):
		start := l.pos
		for l.pos < len(l.input) && isIdentPart(l.input[l.pos]) {
			l.advance()
		}
		word := l.input[start:l.pos]
		if upper := strings.ToUpper(word); keywords[upper] {
			return Token{Kind: Keyword, Text: upper, Line: line, Col: col}, nil
		}
		return Token{Kind: Ident, Text: word, Line: line, Col: col}, nil

	case isDigit(c) || (c == '.' && isDigit(l.peekByte(1))):
		start := l.pos
		for l.pos < len(l.input) && isDigit(l.input[l.pos]) {
			l.advance()
		}
		if l.pos < len(l.input) && l.input[l.pos] == '.' {
			l.advance()
			for l.pos < len(l.input) && isDigit(l.input[l.pos]) {
				l.advance()
			}
		}
		if l.pos < len(l.input) && (l.input[l.pos] == 'e' || l.input[l.pos] == 'E') {
			mark := l.pos
			l.advance()
			if l.pos < len(l.input) && (l.input[l.pos] == '+' || l.input[l.pos] == '-') {
				l.advance()
			}
			if l.pos < len(l.input) && isDigit(l.input[l.pos]) {
				for l.pos < len(l.input) && isDigit(l.input[l.pos]) {
					l.advance()
				}
			} else {
				l.pos, l.col = mark, col+(mark-start)
			}
		}
		return Token{Kind: Number, Text: l.input[start:l.pos], Line: line, Col: col}, nil

	case c == '\'':
		l.advance()
		var sb strings.Builder
		for {
			if l.pos >= len(l.input) {
				return Token{}, l.errorf(line, col, "unterminated string literal")
			}
			ch := l.advance()
			if ch != '\'' {
				sb.WriteByte(ch)
				continue
			}
			if l.pos < len(l.input) && l.input[l.pos] == '\'' {
				l.advance()
				sb.WriteByte('\'')
				continue
			}
			break
		}
		return Token{Kind: String, Text: sb.String(), Line: line, Col: col}, nil

	case c == '"':
		l.advance()
		start := l.pos
		for l.pos < len(l.input) && l.input[l.pos] != '"' {
			l.advance()
		}
		if l.pos >= len(l.input) {
			return Token{}, l.errorf(line, col, "unterminated quoted identifier")
		}
		name := l.input[start:l.pos]
		l.advance()
		return Token{Kind: Ident, Text: name, Line: line, Col: col}, nil
	}

	two := ""
	if l.pos+1 < len(l.input) {
		two = l.input[l.pos : l.pos+2]
	}
	switch two {
	case "<=", ">=", "!=", "<>", "==", "||":
		l.advance()
		l.advance()
		if two == "==" {
			two = "="
		}
		if two == "<>" {
			two = "!="
		}
		return Token{Kind: Operator, Text: two, Line: line, Col: col}, nil
	}

	if strings.IndexByte("=<>+-*/%(),.;", c) >= 0 {
		l.advance()
		return Token{Kind: Operator, Text: string(c), Line: line, Col: col}, nil
	}
	return Token{}, l.errorf(line, col, "unexpected character %q", string(c))
}

// literalFromNumber turns a numeric token into a value.
func literalFromNumber(tok Token) (types.Value, error) {
	if !strings.ContainsAny(tok.Text, ".eE") {
		if i, err := strconv.ParseInt(tok.Text, 10, 64); err == nil {
			return types.NewInt(i), nil
		}
	}
	f, err := strconv.ParseFloat(tok.Text, 64)
	if err != nil {
		return types.NullValue, &SyntaxError{Message: "invalid number " + tok.Text, Line: tok.Line, Col: tok.Col}
	}
	return types.NewFloat(f), nil
}
