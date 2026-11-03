package obsdconf

import (
	"net/netip"
	"testing"
)

// parseValue parses statements of the form "x VALUE" and calls value with
// the current token at VALUE.
func parseValue(src string, value func(p *Parser) bool) error {
	p := New("test.conf", []byte(src), Options{})
	return p.Parse(func() bool {
		p.Next() // "x"
		return value(p)
	})
}

func TestPrefix(t *testing.T) {
	tests := []struct {
		src  string
		want string
	}{
		{"x 192.0.2.0/24\n", "192.0.2.0/24"},
		{"x 2001:db8::/32\n", "2001:db8::/32"},
		{"x 192.0.2.1/32\n", "192.0.2.1/32"},
		// "/" ends a macro name but not the word.
		{"net = 192.0.2.0\nx $net/24\n", "192.0.2.0/24"},
		{"v6 = \"2001:db8:1::\"\nx $v6/48\n", "2001:db8:1::/48"},
		{"lan = 192.0.2.0/24\nx $lan\n", "192.0.2.0/24"},
		{"net = 192.0.2.0\nbits = 24\nx $net/$bits\n", "192.0.2.0/24"},
	}
	for _, tt := range tests {
		var got netip.Prefix
		err := parseValue(tt.src, func(p *Parser) bool {
			var ok bool
			got, ok = p.Prefix()
			return ok
		})
		if err != nil || got.String() != tt.want {
			t.Errorf("%q: got %v, err = %v; want %s", tt.src, got, err, tt.want)
		}
	}
}

func TestPrefixErrors(t *testing.T) {
	tests := map[string]string{
		"x 192.0.2.1/24\n":     `test.conf:1: network 192.0.2.1/24 has host bits set, did you mean 192.0.2.0/24?`,
		"x 192.0.2.0\n":        `test.conf:1: invalid network "192.0.2.0"`,
		"x 192.0.2.0/33\n":     `test.conf:1: invalid network "192.0.2.0/33"`,
		"x \"192.0.2.0/24\"\n": `test.conf:1: expected network, got "192.0.2.0/24"`,
		"x\n":                  `test.conf:1: expected network, got end of line`,
	}
	for src, want := range tests {
		err := parseValue(src, func(p *Parser) bool {
			_, ok := p.Prefix()
			return ok
		})
		if err == nil || err.Error() != want {
			t.Errorf("%q: err = %v, want %s", src, err, want)
		}
	}
}

func TestAddr(t *testing.T) {
	for src, want := range map[string]string{
		"x 192.0.2.1\n":               "192.0.2.1",
		"x 2001:db8::1\n":             "2001:db8::1",
		"a = 192.0.2.1\nx $a\n":       "192.0.2.1",
		"p = \"2001:db8:\"\nx $p:1\n": "2001:db8::1",
	} {
		var got netip.Addr
		err := parseValue(src, func(p *Parser) bool {
			var ok bool
			got, ok = p.Addr()
			return ok
		})
		if err != nil || got.String() != want {
			t.Errorf("%q: got %v, err = %v; want %s", src, got, err, want)
		}
	}
	for src, want := range map[string]string{
		"x fe80::1%em0\n":   `test.conf:1: invalid address "fe80::1%em0"`,
		"x 192.0.2.0/24\n":  `test.conf:1: invalid address "192.0.2.0/24"`,
		"x 192.0.2.256\n":   `test.conf:1: invalid address "192.0.2.256"`,
		"x { 192.0.2.1 }\n": `test.conf:1: expected address, got "{"`,
	} {
		err := parseValue(src, func(p *Parser) bool {
			_, ok := p.Addr()
			return ok
		})
		if err == nil || err.Error() != want {
			t.Errorf("%q: err = %v, want %s", src, err, want)
		}
	}
}

func TestBool(t *testing.T) {
	for src, want := range map[string]bool{"x yes\n": true, "x no\n": false} {
		var got bool
		err := parseValue(src, func(p *Parser) bool {
			var ok bool
			got, ok = p.Bool()
			return ok
		})
		if err != nil || got != want {
			t.Errorf("%q: got %v, err = %v; want %v", src, got, err, want)
		}
	}
	err := parseValue("x true\n", func(p *Parser) bool {
		_, ok := p.Bool()
		return ok
	})
	if want := `test.conf:1: expected "yes" or "no", got "true"`; err == nil || err.Error() != want {
		t.Errorf("err = %v, want %s", err, want)
	}
}
