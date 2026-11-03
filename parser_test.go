package obsdconf

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// conf is the result of the test grammar below.
type conf struct {
	level    string
	clientID string
	interval time.Duration
	servers  []server
	tags     []string
	nosandbx bool
}

type server struct {
	addr netip.Addr
	port uint16
	user string
}

// parseTest parses a small grammar in the style of the MQTT bridges:
//
//	log level debug|info
//	client id NAME
//	interval DURATION
//	no sandbox
//	tags NAME|{ NAME ... }
//	server ADDRESS [port N] [{ user NAME }]
func parseTest(src string, opts Options) (conf, error) {
	var c conf
	p := New("test.conf", []byte(src), opts)
	err := p.Parse(func() bool {
		t := p.Tok()
		var ok bool
		switch {
		case p.Accept("log", "level"):
			c.level, ok = p.Enum("log level", "debug", "info")
			return ok
		case p.Accept("client", "id"):
			c.clientID, ok = p.Text("client id")
			return ok
		case p.Accept("interval"):
			c.interval, ok = p.Duration(time.Millisecond, time.Hour)
			return ok
		case p.Accept("no", "sandbox"):
			c.nosandbx = true
			return true
		case p.Accept("tags"):
			return p.List(func() bool {
				s, ok := p.Text("tag")
				c.tags = append(c.tags, s)
				return ok
			})
		case p.Accept("server"):
			var s server
			if s.addr, ok = p.Addr(); !ok {
				return false
			}
			if p.Accept("port") {
				if s.port, ok = p.Port(); !ok {
					return false
				}
			}
			if p.Tok().Kind == LBrace {
				ok = p.Block("server", func() bool {
					if p.Accept("user") {
						s.user, ok = p.Text("user name")
						return ok
					}
					p.Errorf(p.Tok().Pos, "unknown server option %q", p.Tok().Text)
					return false
				})
			}
			c.servers = append(c.servers, s)
			return ok
		}
		p.Errorf(t.Pos, "unknown statement %q", t.Text)
		return false
	})
	return c, err
}

func TestParse(t *testing.T) {
	c, err := parseTest(`
# Comment
log level debug
client id "bridge 1"
interval 1m30s
no sandbox
srv = "{ 192.0.2.1 2001:db8::1 }"
tags $srv
tags { a, b
       "c d" }
server 192.0.2.1 port 1883 {
	user mqtt
}
server ::1
`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := conf{
		level:    "debug",
		clientID: "bridge 1",
		interval: 90 * time.Second,
		nosandbx: true,
		tags:     []string{"192.0.2.1", "2001:db8::1", "a", "b", "c d"},
		servers: []server{
			{addr: netip.MustParseAddr("192.0.2.1"), port: 1883, user: "mqtt"},
			{addr: netip.MustParseAddr("::1")},
		},
	}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("got  %+v\nwant %+v", c, want)
	}
}

func TestPredefinedMacros(t *testing.T) {
	c, err := parseTest("client id $id\n", Options{Macros: map[string]string{"id": "from-cli"}})
	if err != nil {
		t.Fatal(err)
	}
	if c.clientID != "from-cli" {
		t.Errorf("client id = %q", c.clientID)
	}
}

func TestParseDuration(t *testing.T) {
	tests := map[string]time.Duration{
		"0":     0,
		"90":    90 * time.Second,
		"500ms": 500 * time.Millisecond,
		"5s":    5 * time.Second,
		"1h30m": 90 * time.Minute,
		"1m5s":  65 * time.Second,
		"2w":    14 * 24 * time.Hour,
		"1d12h": 36 * time.Hour,
	}
	for s, want := range tests {
		if got, ok := parseDuration(s); !ok || got != want {
			t.Errorf("parseDuration(%q) = %v, %v; want %v", s, got, ok, want)
		}
	}
	for _, s := range []string{"", "s", "1.5s", "5x", "h1", "-1s", "99999999999w"} {
		if got, ok := parseDuration(s); ok {
			t.Errorf("parseDuration(%q) = %v, want error", s, got)
		}
	}
}

func TestInclude(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string, mode os.FileMode) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(src), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		return path
	}
	main := write("main.conf", "include \"secret.conf\"\nclient id $id\n", 0o644)
	write("secret.conf", "id = from-include", 0o600) // no final newline

	p, err := NewFile(main, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var id string
	err = p.Parse(func() bool {
		p.Expect("client", "id")
		var ok bool
		id, ok = p.Text("id")
		return ok
	})
	if err != nil || id != "from-include" {
		t.Fatalf("id = %q, err = %v", id, err)
	}

	loop := write("loop.conf", "include \"loop.conf\"\n", 0o644)
	p, err = NewFile(loop, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Parse(func() bool { return true }); err == nil || !strings.Contains(err.Error(), "include loop") {
		t.Errorf("include loop: err = %v", err)
	}
}

func TestSecret(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.conf")
	if err := os.WriteFile(path, []byte("include \"inc.conf\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	inc := filepath.Join(dir, "inc.conf")
	if err := os.WriteFile(inc, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(inc, 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := NewFile(path, Options{Secret: true})
	if err != nil {
		t.Fatalf("NewFile(0600): %v", err)
	}
	err = p.Parse(func() bool { return true })
	if err == nil || !strings.Contains(err.Error(), "group writable or world read/writable") {
		t.Errorf("world-readable include: err = %v", err)
	}

	// Group read is allowed, as in OpenBSD.
	if err := os.Chmod(inc, 0o640); err != nil {
		t.Fatal(err)
	}
	p, _ = NewFile(path, Options{Secret: true})
	if err := p.Parse(func() bool { return true }); err != nil {
		t.Errorf("0640 include: %v", err)
	}

	if err := os.Chmod(path, 0o604); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFile(path, Options{Secret: true}); err == nil {
		t.Error("NewFile accepted a world-readable file")
	}
	if _, err := NewFile(path, Options{}); err != nil {
		t.Errorf("NewFile without Secret: %v", err)
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		opts Options
		want []string
	}{
		{"unknown statement", "frobnicate\n", Options{}, []string{
			`test.conf:1: unknown statement "frobnicate"`}},
		{"enum", "log level loud\n", Options{}, []string{
			`test.conf:1: expected log level, got "loud"`}},
		{"duration range", "interval 2h\n", Options{}, []string{
			`test.conf:1: duration "2h" out of range (1ms-3600s)`}},
		{"port range", "server ::1 port 0\n", Options{}, []string{
			`test.conf:1: number 0 out of range (1-65535)`}},
		{"undefined macro", "client id $x\n", Options{}, []string{
			`test.conf:1: undefined macro "$x"`}},
		{"keyword macro", "server = 1\n", Options{Keywords: []string{"server"}}, []string{
			`test.conf:1: "server" is a keyword and cannot be used as a macro name`}},
		{"include is a keyword", "include = 1\n", Options{}, []string{
			`test.conf:1: "include" is a keyword and cannot be used as a macro name`}},
		{"empty list", "tags { }\n", Options{}, []string{
			`test.conf:1: empty list`}},
		{"unterminated list", "tags { a\n", Options{}, []string{
			`test.conf:1: unterminated list`}},
		{"trailing token", "no sandbox now\n", Options{}, []string{
			`test.conf:1: expected end of line, got "now"`}},
		{"missing brace", "server ::1 {\n\tuser x\n", Options{}, []string{
			`test.conf:1: server: missing "}"`}},
		{"stray brace", "}\nno sandbox\n", Options{}, []string{
			`test.conf:1: expected statement, got "}"`}},
		{"recovery in block", "server ::1 {\n\tuser\n\tgroup x\n\tuser ok\n}\nlog level x\n", Options{}, []string{
			`test.conf:2: expected user name, got end of line`,
			`test.conf:3: unknown server option "group"`,
			`test.conf:6: expected log level, got "x"`}},
		{"lexical error reported once", "client id \"abc\n", Options{}, []string{
			`test.conf:1: unterminated string`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseTest(tt.src, tt.opts)
			var list ErrorList
			if !errors.As(err, &list) {
				t.Fatalf("err = %v, want ErrorList", err)
			}
			var got []string
			for _, e := range list {
				got = append(got, e.Error())
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(tt.want, "\n  "))
			}
		})
	}
}

func TestUnusedMacros(t *testing.T) {
	p := New("test.conf", []byte(`
used = "client"
helper = x
chain = "$helper y"
unify = 192.0.2.10
also = 1
log level debug
client id $used
tags $chain
`), Options{Macros: map[string]string{"cli": "unused-but-predefined"}})
	if err := p.Parse(func() bool {
		for p.Tok().Kind == Word {
			p.Next()
		}
		return true
	}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range p.UnusedMacros() {
		got = append(got, fmt.Sprintf("%s %s", m.Pos, m.Name))
	}
	if want := []string{"test.conf:5 unify", "test.conf:6 also"}; !reflect.DeepEqual(got, want) {
		t.Errorf("unused = %v, want %v", got, want)
	}
}

func TestEmptyBlockOnOneLine(t *testing.T) {
	for _, src := range []string{"server ::1 {}\n", "server ::1 { }\n", "server ::1 {\n}\n"} {
		c, err := parseTest(src, Options{})
		if err != nil || len(c.servers) != 1 {
			t.Errorf("%q: servers = %v, err = %v", src, c.servers, err)
		}
	}
	// One statement may share the line, as in httpd.conf.
	c, err := parseTest("server ::1 { user x }\nno sandbox\n", Options{})
	if err != nil || c.servers[0].user != "x" || !c.nosandbx {
		t.Errorf("one-line block: %+v, err = %v", c, err)
	}
	// Two may not.
	_, err = parseTest("server ::1 { user x user y }\nno sandbox\n", Options{})
	if err == nil || err.Error() != `test.conf:1: expected "}": a block on one line holds one statement, got "user"` {
		t.Errorf("two statements on one line: err = %v", err)
	}
	// An error inside reports once and goes on after the line.
	_, err = parseTest("server ::1 { group x }\nlog level loud\n", Options{})
	if err == nil || err.Error() != "test.conf:1: unknown server option \"group\"\ntest.conf:2: expected log level, got \"loud\"" {
		t.Errorf("error in one-line block: err = %v", err)
	}
}

func TestMacroInWord(t *testing.T) {
	tests := []struct {
		name string
		src  string
		opts Options
		want string
	}{
		{"quoted value", "movies = \"/export/movies\"\nclient id $movies/plex\n", Options{}, "/export/movies/plex"},
		{"word value", "movies = /export/movies\nclient id $movies/plex\n", Options{}, "/export/movies/plex"},
		{"name ends at dot", "host = example\nclient id $host.org\n", Options{}, "example.org"},
		{"name includes underscore and digits", "h_1 = x\nh = y\nclient id $h_1-$h\n", Options{}, "x-y"},
		{"text before", "a = x\nclient id pre-$a\n", Options{}, "pre-x"},
		{"two references", "a = x\nb = y\nclient id $a$b\n", Options{}, "xy"},
		{"host and port", "host = example.org\nport = 1883\nclient id $host:$port\n", Options{}, "example.org:1883"},
		{"in quoted macro value", "base = /export\nmovies = \"$base/movies\"\nclient id $movies/plex\n", Options{}, "/export/movies/plex"},
		{"in word macro value", "base = /export\nmovies = $base/movies\nclient id $movies/plex\n", Options{}, "/export/movies/plex"},
		{"predefined", "client id $root/x\n", Options{Macros: map[string]string{"root": "/r"}}, "/r/x"},
		{"not in quoted string", "movies = /export/movies\nclient id \"$movies/plex\"\n", Options{}, "$movies/plex"},
		{"quoted string value alone", "q = \"\\\"a b\\\"\"\nclient id $q\n", Options{}, "a b"},
		{"braces before name character", "base = /export\nclient id ${base}_old\n", Options{}, "/export_old"},
		{"braces before digit", "host = node\nclient id ${host}1\n", Options{}, "node1"},
		{"braces alone", "a = x\nclient id ${a}\n", Options{}, "x"},
		{"braces twice", "a = x\nb = y\nclient id ${a}${b}\n", Options{}, "xy"},
		{"braces mixed", "a = x\nb = y\nclient id ${a}_$b.z\n", Options{}, "x_y.z"},
		{"braces in macro value", "base = /export\nold = \"${base}_old\"\nclient id $old/plex\n", Options{}, "/export_old/plex"},
		{"braces not in quoted string", "a = x\nclient id \"${a}\"\n", Options{}, "${a}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := parseTest(tt.src, tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			if c.clientID != tt.want {
				t.Errorf("client id = %q, want %q", c.clientID, tt.want)
			}
		})
	}
}

func TestMacroInWordList(t *testing.T) {
	// A reference that is the whole word still expands to a list; one
	// that is part of a word may be a list item.
	c, err := parseTest(`
dir = /srv
lan = "{ a b }"
tags $lan
tags { $dir/x, $dir/y }
tags ${lan}
tags { ${dir}_z }
`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b", "/srv/x", "/srv/y", "a", "b", "/srv_z"}; !reflect.DeepEqual(c.tags, want) {
		t.Errorf("tags = %q, want %q", c.tags, want)
	}
}

func TestMacroInWordErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"undefined", "client id $movies/plex\n", []string{
			`test.conf:1: undefined macro "$movies"`}},
		{"undefined second", "a = x\nclient id $a:$b\n", []string{
			`test.conf:2: undefined macro "$b"`}},
		{"list before text", "l = \"{ a b }\"\nclient id $l/x\nlog level loud\n", []string{
			`test.conf:2: macro "$l" does not expand to a single word, so it cannot be part of the word "$l/x"`,
			`test.conf:3: expected log level, got "loud"`}},
		{"list after text", "l = { a b }\ntags x$l\n", []string{
			`test.conf:2: macro "$l" does not expand to a single word, so it cannot be part of the word "x$l"`}},
		{"two words", "l = \"a b\"\nclient id $l.x\n", []string{
			`test.conf:2: macro "$l" does not expand to a single word, so it cannot be part of the word "$l.x"`}},
		{"quoted string", "q = \"\\\"a b\\\"\"\nclient id $q/x\n", []string{
			`test.conf:2: macro "$q" does not expand to a single word, so it cannot be part of the word "$q/x"`}},
		{"list in macro value", "l = \"{ a b }\"\nm = \"$l/x\"\n", []string{
			`test.conf:2: macro "$l" does not expand to a single word, so it cannot be part of the word "$l/x"`}},
		{"undefined in macro value", "m = \"$base/x\"\n", []string{
			`test.conf:1: undefined macro "$base"`}},
		{"dollar at end", "client id a$\n", []string{
			`test.conf:1: missing macro name after "$" in "a$" (quote the value for a literal "$")`}},
		{"dollar before slash", "client id $/x\n", []string{
			`test.conf:1: missing macro name after "$" in "$/x" (quote the value for a literal "$")`}},
		{"dollar alone", "client id $\n", []string{
			`test.conf:1: missing macro name after "$" in "$" (quote the value for a literal "$")`}},
		{"dollar dollar", "a = x\nclient id $$a\n", []string{
			`test.conf:2: missing macro name after "$" in "$$a" (quote the value for a literal "$")`}},
		{"name starting with digit", "client id $1x\n", []string{
			`test.conf:1: undefined macro "$1x"`}},
		{"braces undefined", "client id ${base}_old\n", []string{
			`test.conf:1: undefined macro "${base}"`}},
		{"braces list", "l = \"{ a b }\"\ntags ${l}_x\n", []string{
			`test.conf:2: macro "${l}" does not expand to a single word, so it cannot be part of the word "${l}_x"`}},
		{"braces empty", "client id ${}\n", []string{
			`test.conf:1: missing macro name after "$" in "${}" (quote the value for a literal "$")`}},
		{"braces with space", "client id ${a b}\nlog level loud\n", []string{
			`test.conf:1: invalid macro reference, expected ${name}`,
			`test.conf:2: expected log level, got "loud"`}},
		{"braces with slash", "client id ${a/b}\n", []string{
			`test.conf:1: invalid macro reference, expected ${name}`}},
		{"braces unterminated", "client id ${a\nlog level loud\n", []string{
			`test.conf:1: invalid macro reference, expected ${name}`,
			`test.conf:2: expected log level, got "loud"`}},
		{"braces unterminated in block", "server ::1 {\n\tuser ${a b}\n\tuser ok\n}\nlog level loud\n", []string{
			`test.conf:2: invalid macro reference, expected ${name}`,
			`test.conf:5: expected log level, got "loud"`}},
		{"braces in macro value", "m = \"${a\"\n", []string{
			`test.conf:1: in macro value: invalid macro reference, expected ${name}`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseTest(tt.src, Options{})
			var list ErrorList
			if !errors.As(err, &list) {
				t.Fatalf("err = %v, want ErrorList", err)
			}
			var got []string
			for _, e := range list {
				got = append(got, e.Error())
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(tt.want, "\n  "))
			}
		})
	}
}

func TestMacroInWordInclude(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write("paths.conf", "movies = \"/export/movies\"\nlist = \"{ a b }\"\n\ntags $list/x\n")
	main := write("main.conf", "include \"paths.conf\"\nclient id $movies/plex\ntags $list/y\n")

	var c conf
	p, err := NewFile(main, Options{})
	if err != nil {
		t.Fatal(err)
	}
	err = p.Parse(func() bool {
		switch {
		case p.Accept("client", "id"):
			var ok bool
			c.clientID, ok = p.Text("client id")
			return ok
		case p.Accept("tags"):
			return p.List(func() bool {
				s, ok := p.Text("tag")
				c.tags = append(c.tags, s)
				return ok
			})
		}
		return false
	})
	if c.clientID != "/export/movies/plex" {
		t.Errorf("client id = %q", c.clientID)
	}
	paths := filepath.Join(dir, "paths.conf")
	want := paths + `:4: macro "$list" does not expand to a single word, so it cannot be part of the word "$list/x"` + "\n" +
		main + `:3: macro "$list" does not expand to a single word, so it cannot be part of the word "$list/y"`
	if err == nil || err.Error() != want {
		t.Errorf("err =\n%v\nwant\n%s", err, want)
	}
}

func TestUnusedMacrosInWords(t *testing.T) {
	p := New("test.conf", []byte(`
base = /export
movies = "$base/movies"
host = example.org
port = 1883
unused = x
suffix = old
path $movies/plex
url $host:$port
backup ${movies}_$suffix
`), Options{})
	if err := p.Parse(func() bool {
		for p.Tok().Kind == Word {
			p.Next()
		}
		return true
	}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range p.UnusedMacros() {
		got = append(got, fmt.Sprintf("%s %s", m.Pos, m.Name))
	}
	if want := []string{"test.conf:6 unused"}; !reflect.DeepEqual(got, want) {
		t.Errorf("unused = %v, want %v", got, want)
	}
}
