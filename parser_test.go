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
