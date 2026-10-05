package obsdconf

import "strconv"

// Kind is the kind of a token.
type Kind int

const (
	EOF Kind = iota
	Newline
	Word
	String
	LBrace
	RBrace
	Equals
	Comma
	// Illegal carries a lexical error message in Text. The parser reports
	// it as soon as the token is read.
	Illegal
)

// Token is a lexical token. Text is the word, the unquoted string, or the
// punctuation character.
type Token struct {
	Kind Kind
	Text string
	Pos  Pos
}

// String describes the token for error messages: "end of line", "end of
// file", or the quoted text.
func (t Token) String() string {
	switch t.Kind {
	case EOF:
		return "end of file"
	case Newline:
		return "end of line"
	}
	return strconv.Quote(t.Text)
}

// lexer splits one file into tokens.
type lexer struct {
	file string
	src  []byte
	off  int
	line int
	// eol is set once the newline that ends the last statement of the
	// file has been returned.
	eol bool
}

func newLexer(file string, src []byte) *lexer {
	return &lexer{file: file, src: src, line: 1}
}

func (l *lexer) pos() Pos {
	return Pos{File: l.file, Line: l.line}
}

func isDelim(c byte) bool {
	switch c {
	case ' ', '\t', '\r', '\n', '#', '{', '}', '"', '=', ',', '\\':
		return true
	}
	return false
}

// next returns the next token. At the end of the input it returns a
// newline, so that the last statement is terminated even without a
// trailing newline, and then EOF.
func (l *lexer) next() Token {
	for {
		if l.off >= len(l.src) {
			if !l.eol {
				l.eol = true
				return Token{Kind: Newline, Pos: l.pos()}
			}
			return Token{Kind: EOF, Pos: l.pos()}
		}
		c := l.src[l.off]
		switch c {
		case ' ', '\t', '\r':
			l.off++
		case '#':
			for l.off < len(l.src) && l.src[l.off] != '\n' {
				l.off++
			}
		case '\\':
			if rest := l.src[l.off+1:]; len(rest) > 0 && rest[0] == '\n' {
				l.off += 2
				l.line++
				continue
			} else if len(rest) > 1 && rest[0] == '\r' && rest[1] == '\n' {
				l.off += 3
				l.line++
				continue
			}
			p := l.pos()
			l.off++
			return Token{Kind: Illegal, Text: `"\" is only allowed at the end of a line`, Pos: p}
		case '\n':
			p := l.pos()
			l.off++
			l.line++
			return Token{Kind: Newline, Pos: p}
		case '{':
			return l.single(LBrace)
		case '}':
			return l.single(RBrace)
		case '=':
			return l.single(Equals)
		case ',':
			return l.single(Comma)
		case '"':
			return l.quoted()
		default:
			return l.word()
		}
	}
}

func (l *lexer) single(kind Kind) Token {
	t := Token{Kind: kind, Text: string(l.src[l.off]), Pos: l.pos()}
	l.off++
	return t
}

func (l *lexer) word() Token {
	start := l.off
	for l.off < len(l.src) && !isDelim(l.src[l.off]) {
		l.off++
	}
	return Token{Kind: Word, Text: string(l.src[start:l.off]), Pos: l.pos()}
}

func (l *lexer) quoted() Token {
	p := l.pos()
	l.off++ // opening quote
	var buf []byte
	badEscape := false
	for {
		if l.off >= len(l.src) || l.src[l.off] == '\n' {
			return Token{Kind: Illegal, Text: "unterminated string", Pos: p}
		}
		c := l.src[l.off]
		l.off++
		switch c {
		case '"':
			if badEscape {
				return Token{Kind: Illegal, Text: `invalid escape in string, only \" and \\ are allowed`, Pos: p}
			}
			return Token{Kind: String, Text: string(buf), Pos: p}
		case '\\':
			if l.off < len(l.src) && (l.src[l.off] == '"' || l.src[l.off] == '\\') {
				buf = append(buf, l.src[l.off])
				l.off++
			} else {
				badEscape = true
			}
		default:
			buf = append(buf, c)
		}
	}
}
