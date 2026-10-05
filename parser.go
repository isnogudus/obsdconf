package obsdconf

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// DefaultMaxIncludeDepth limits how deeply include statements may nest
// if Options.MaxIncludeDepth is zero.
const DefaultMaxIncludeDepth = 16

// Pos is a position in a configuration file.
type Pos struct {
	File string
	Line int
}

func (p Pos) String() string {
	return fmt.Sprintf("%s:%d", p.File, p.Line)
}

// Error is a problem found at a position in the configuration.
type Error struct {
	Pos Pos
	Msg string
}

func (e *Error) Error() string {
	return e.Pos.String() + ": " + e.Msg
}

// ErrorList holds all errors of a parse, in the order they were found.
type ErrorList []*Error

func (l ErrorList) Error() string {
	msgs := make([]string, len(l))
	for i, e := range l {
		msgs[i] = e.Error()
	}
	return strings.Join(msgs, "\n")
}

// Options configure a Parser.
type Options struct {
	// Keywords of the grammar. They cannot be used as macro names.
	Keywords []string
	// Macros predefines macros, like pfctl -D name=value. Values are split
	// into tokens like quoted macro values.
	Macros map[string]string
	// Secret requires every file read, including included ones, to be
	// owned by root or the current user and not to be group writable or
	// accessible by others, like check_file_secrecy() in OpenBSD daemons.
	// Use it for configurations that hold passwords or keys. Standard
	// input is not checked.
	Secret bool
	// MaxIncludeDepth limits nested includes; 0 means
	// DefaultMaxIncludeDepth.
	MaxIncludeDepth int
}

var macroName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Parser reads a configuration as a sequence of statements. It handles
// macros, include, lists, blocks and recovery after errors; the grammar
// of the statements is up to the caller.
//
// A statement function is called with the first word of a statement as
// the current token. It consumes the statement, but not the newline that
// ends it, and returns false after reporting an error with Errorf or
// Expected. The parser then skips the rest of the statement and goes on,
// so that one run reports every error.
type Parser struct {
	opts   Options
	lexers []*lexer
	// active holds the absolute paths of the files being read, to detect
	// include loops.
	active map[string]bool
	// pending holds the remaining tokens of a macro expansion.
	pending  []Token
	tok      Token
	ahead    []Token
	macros   map[string][]Token
	keywords map[string]bool
	errs     ErrorList
}

// NewFile returns a parser for the file at path. A path of "-" reads
// standard input.
func NewFile(path string, opts Options) (*Parser, error) {
	if path == "-" {
		src, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, err
		}
		return New("-", src, opts), nil
	}
	if opts.Secret {
		if err := checkSecrecy(path); err != nil {
			return nil, err
		}
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return New(path, src, opts), nil
}

// New returns a parser for src. name is used in error messages and as the
// base for relative include paths.
func New(name string, src []byte, opts Options) *Parser {
	if opts.MaxIncludeDepth == 0 {
		opts.MaxIncludeDepth = DefaultMaxIncludeDepth
	}
	p := &Parser{
		opts:     opts,
		active:   map[string]bool{},
		macros:   map[string][]Token{},
		keywords: map[string]bool{"include": true},
	}
	for _, k := range opts.Keywords {
		p.keywords[k] = true
	}
	for name, value := range opts.Macros {
		t := Token{Kind: String, Text: value, Pos: Pos{File: "command line"}}
		if !macroName.MatchString(name) {
			p.Errorf(t.Pos, "invalid macro name %q", name)
			continue
		}
		if toks, ok := p.relex(t); ok {
			p.macros[name] = toks
		}
	}
	p.push(name, src)
	p.Next()
	return p
}

func absPath(name string) string {
	if a, err := filepath.Abs(name); err == nil {
		return a
	}
	return name
}

func (p *Parser) push(name string, src []byte) {
	p.lexers = append(p.lexers, newLexer(name, src))
	p.active[absPath(name)] = true
}

// raw returns the next token of the innermost file and continues with the
// including file at the end of an included one.
func (p *Parser) raw() Token {
	for {
		l := p.lexers[len(p.lexers)-1]
		t := l.next()
		if t.Kind != EOF || len(p.lexers) == 1 {
			return t
		}
		delete(p.active, absPath(l.file))
		p.lexers = p.lexers[:len(p.lexers)-1]
	}
}

// fetch returns the next token with macros expanded and reports lexical
// errors.
func (p *Parser) fetch() Token {
	for {
		var t Token
		if len(p.pending) > 0 {
			t, p.pending = p.pending[0], p.pending[1:]
		} else {
			t = p.raw()
		}
		if t.Kind == Word && strings.HasPrefix(t.Text, "$") {
			val, ok := p.macros[t.Text[1:]]
			if !ok {
				t = Token{Kind: Illegal, Text: fmt.Sprintf("undefined macro %q", t.Text), Pos: t.Pos}
			} else {
				exp := make([]Token, len(val), len(val)+len(p.pending))
				for i, v := range val {
					v.Pos = t.Pos
					exp[i] = v
				}
				p.pending = append(exp, p.pending...)
				continue
			}
		}
		if t.Kind == Illegal {
			p.Errorf(t.Pos, "%s", t.Text)
		}
		return t
	}
}

// Tok returns the current token.
func (p *Parser) Tok() Token {
	return p.tok
}

// Next moves to the next token.
func (p *Parser) Next() {
	if len(p.ahead) > 0 {
		p.tok, p.ahead = p.ahead[0], p.ahead[1:]
		return
	}
	p.tok = p.fetch()
}

// Peek returns the token n positions after the current one; Peek(1) is
// the next token.
func (p *Parser) Peek(n int) Token {
	for len(p.ahead) < n {
		p.ahead = append(p.ahead, p.fetch())
	}
	return p.ahead[n-1]
}

// Is reports whether the next tokens are the given words, starting with
// the current token.
func (p *Parser) Is(words ...string) bool {
	for i, w := range words {
		t := p.tok
		if i > 0 {
			t = p.Peek(i)
		}
		if t.Kind != Word || t.Text != w {
			return false
		}
	}
	return len(words) > 0
}

// Accept consumes the given words if they come next, as in
// p.Accept("client", "id"), and reports whether it did.
func (p *Parser) Accept(words ...string) bool {
	if !p.Is(words...) {
		return false
	}
	for range words {
		p.Next()
	}
	return true
}

// Expect is Accept, but reports an error if the words do not come next.
func (p *Parser) Expect(words ...string) bool {
	if p.Accept(words...) {
		return true
	}
	return p.Expected(fmt.Sprintf("%q", strings.Join(words, " ")))
}

// Errorf reports an error at pos.
func (p *Parser) Errorf(pos Pos, format string, args ...any) {
	p.errs = append(p.errs, &Error{Pos: pos, Msg: fmt.Sprintf(format, args...)})
}

// Expected reports that the current token is not what is expected here.
// Lexical errors have already been reported when the token was read. It
// always returns false.
func (p *Parser) Expected(what string) bool {
	if p.tok.Kind != Illegal {
		p.Errorf(p.tok.Pos, "expected %s, got %s", what, p.tok)
	}
	return false
}

// Errors returns the errors reported so far.
func (p *Parser) Errors() ErrorList {
	return p.errs
}

// skipStatement skips the rest of a statement after an error, including
// any block it opened. Inside a block it stops before the closing brace of
// that block.
func (p *Parser) skipStatement(inBlock bool) {
	depth := 0
	for {
		switch p.tok.Kind {
		case EOF:
			return
		case Newline:
			if depth == 0 {
				p.Next()
				return
			}
		case LBrace:
			depth++
		case RBrace:
			if depth == 0 && inBlock {
				return
			}
			if depth > 0 {
				depth--
			}
		}
		p.Next()
	}
}

func (p *Parser) endOfStatement() bool {
	switch p.tok.Kind {
	case Newline:
		p.Next()
		return true
	case EOF:
		// Only reached after an unclosed block, which is reported already.
		return true
	}
	return p.Expected("end of line")
}

// Parse reads the whole input. Macro definitions and include statements
// are handled by the parser; every other statement is passed to stmt. It
// returns an ErrorList if any error was reported.
func (p *Parser) Parse(stmt func() bool) error {
	for p.tok.Kind != EOF {
		if p.tok.Kind == Newline {
			p.Next()
			continue
		}
		if !p.topLevel(stmt) || !p.endOfStatement() {
			p.skipStatement(false)
		}
	}
	if len(p.errs) > 0 {
		return p.errs
	}
	return nil
}

func (p *Parser) topLevel(stmt func() bool) bool {
	if p.tok.Kind != Word {
		return p.Expected("statement")
	}
	if p.Peek(1).Kind == Equals {
		return p.macroDef()
	}
	if p.tok.Text == "include" {
		return p.include()
	}
	return stmt()
}

func (p *Parser) macroDef() bool {
	t := p.tok
	if !macroName.MatchString(t.Text) {
		p.Errorf(t.Pos, "invalid macro name %q", t.Text)
		return false
	}
	if p.keywords[t.Text] {
		p.Errorf(t.Pos, "%q is a keyword and cannot be used as a macro name", t.Text)
		return false
	}
	p.Next() // name
	p.Next() // "="
	var val []Token
	switch p.tok.Kind {
	case Word:
		val = []Token{p.tok}
		p.Next()
	case String:
		toks, ok := p.relex(p.tok)
		if !ok {
			return false
		}
		val = toks
		p.Next()
	case LBrace:
		toks, ok := p.collectList()
		if !ok {
			return false
		}
		val = toks
	default:
		return p.Expected("macro value")
	}
	if len(val) == 0 {
		p.Errorf(t.Pos, "macro %q has an empty value", t.Text)
		return false
	}
	p.macros[t.Text] = val
	return true
}

// relex splits the contents of a quoted macro value into tokens, as
// pf.conf does, so that "{ a b }" defines a list.
func (p *Parser) relex(t Token) ([]Token, bool) {
	l := newLexer(t.Pos.File, []byte(t.Text))
	var out []Token
	for {
		u := l.next()
		switch u.Kind {
		case EOF:
			return out, true
		case Newline:
			continue
		case Illegal:
			p.Errorf(t.Pos, "in macro value: %s", u.Text)
			return nil, false
		}
		if u.Kind == Word && strings.HasPrefix(u.Text, "$") {
			val, ok := p.macros[u.Text[1:]]
			if !ok {
				p.Errorf(t.Pos, "undefined macro %q", u.Text)
				return nil, false
			}
			out = append(out, val...)
			continue
		}
		u.Pos = t.Pos
		out = append(out, u)
	}
}

// collectList returns the tokens of a brace-enclosed list, braces included.
func (p *Parser) collectList() ([]Token, bool) {
	open := p.tok
	var out []Token
	depth := 0
	for {
		switch p.tok.Kind {
		case EOF:
			p.Errorf(open.Pos, "unterminated list")
			return nil, false
		case Illegal:
			return nil, false
		case Newline:
			p.Next()
			continue
		case LBrace:
			depth++
		case RBrace:
			depth--
		}
		out = append(out, p.tok)
		p.Next()
		if depth == 0 {
			return out, true
		}
	}
}

func (p *Parser) include() bool {
	kw := p.tok
	p.Next()
	if p.tok.Kind != String {
		return p.Expected("quoted file name")
	}
	path := p.tok.Text
	p.Next()
	if p.tok.Kind != Newline {
		return p.Expected("end of line")
	}
	if !filepath.IsAbs(path) && kw.Pos.File != "-" {
		path = filepath.Join(filepath.Dir(kw.Pos.File), path)
	}
	if p.active[absPath(path)] {
		p.Errorf(kw.Pos, "include loop: %s", path)
		return false
	}
	if len(p.lexers) >= p.opts.MaxIncludeDepth {
		p.Errorf(kw.Pos, "include: nested more than %d levels deep", p.opts.MaxIncludeDepth)
		return false
	}
	if p.opts.Secret {
		if err := checkSecrecy(path); err != nil {
			p.Errorf(kw.Pos, "include: %v", err)
			return false
		}
	}
	src, err := os.ReadFile(path)
	if err != nil {
		p.Errorf(kw.Pos, "include: %v", err)
		return false
	}
	// The newline ending this statement is the current token, so the next
	// token is read from the included file.
	p.push(path, src)
	return true
}

// List parses a single item or a brace-enclosed list of items. Newlines
// and commas between items are ignored. item is called with the current
// token at the item and consumes it.
func (p *Parser) List(item func() bool) bool {
	if p.tok.Kind != LBrace {
		return item()
	}
	open := p.tok
	p.Next()
	n := 0
	for {
		switch p.tok.Kind {
		case Newline, Comma:
			p.Next()
			continue
		case RBrace:
			p.Next()
			if n == 0 {
				p.Errorf(open.Pos, "empty list")
				return false
			}
			return true
		case EOF:
			p.Errorf(open.Pos, "unterminated list")
			return false
		}
		if !item() {
			p.skipList()
			return false
		}
		n++
	}
}

// skipList skips past the closing brace of a list after a failed item.
func (p *Parser) skipList() {
	for p.tok.Kind != EOF {
		k := p.tok.Kind
		p.Next()
		if k == RBrace {
			return
		}
	}
}

// Block parses the statements of a brace-enclosed block; the opening
// brace is the current token and must end its line. stmt is called like
// the statement function of Parse. what names the block in the error for
// a missing closing brace.
func (p *Parser) Block(what string, stmt func() bool) bool {
	open := p.tok
	if open.Kind != LBrace {
		return p.Expected(`"{"`)
	}
	p.Next()
	if p.tok.Kind != Newline {
		// Report, but keep parsing the block.
		p.Expected(`end of line after "{"`)
	}
	for {
		switch p.tok.Kind {
		case Newline:
			p.Next()
			continue
		case RBrace:
			p.Next()
			return true
		case EOF:
			p.Errorf(open.Pos, `%s: missing "}"`, what)
			return false
		}
		ok := false
		if p.tok.Kind == Word {
			ok = stmt()
		} else {
			p.Expected("statement")
		}
		if !ok || !p.endOfStatement() {
			p.skipStatement(true)
		}
	}
}
