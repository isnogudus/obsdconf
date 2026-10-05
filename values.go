package obsdconf

import (
	"fmt"
	"math"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// Word consumes a word and returns it. what describes the expected value
// in the error message.
func (p *Parser) Word(what string) (string, bool) {
	if p.tok.Kind != Word {
		return "", p.Expected(what)
	}
	s := p.tok.Text
	p.Next()
	return s, true
}

// Text consumes a word or a quoted string and returns its text. A quoted
// string is never taken as a keyword, so it can hold any value, including
// spaces, braces and UTF-8. Empty strings are rejected.
func (p *Parser) Text(what string) (string, bool) {
	t := p.tok
	if t.Kind != Word && t.Kind != String {
		return "", p.Expected(what)
	}
	p.Next()
	if t.Text == "" {
		p.Errorf(t.Pos, "empty %s", what)
		return "", false
	}
	return t.Text, true
}

// Number consumes a decimal number in the range min to max.
func (p *Parser) Number(min, max uint64) (uint64, bool) {
	t := p.tok
	if _, ok := p.Word("number"); !ok {
		return 0, false
	}
	n, err := strconv.ParseUint(t.Text, 10, 64)
	if err != nil {
		p.Errorf(t.Pos, "invalid number %q", t.Text)
		return 0, false
	}
	if n < min || n > max {
		p.Errorf(t.Pos, "number %d out of range (%d-%d)", n, min, max)
		return 0, false
	}
	return n, true
}

// Port consumes a port number from 1 to 65535.
func (p *Parser) Port() (uint16, bool) {
	n, ok := p.Number(1, math.MaxUint16)
	return uint16(n), ok
}

var durationUnits = []struct {
	suffix string
	unit   time.Duration
}{
	// Longer suffixes first, so that "ms" is not read as "m".
	{"ms", time.Millisecond},
	{"s", time.Second},
	{"m", time.Minute},
	{"h", time.Hour},
	{"d", 24 * time.Hour},
	{"w", 7 * 24 * time.Hour},
}

// parseDuration reads a plain number of seconds or a sequence of numbers
// with units, as in 90, 1h30m or 500ms.
func parseDuration(s string) (time.Duration, bool) {
	if n, err := strconv.ParseUint(s, 10, 32); err == nil {
		return time.Duration(n) * time.Second, true
	}
	if s == "" {
		return 0, false
	}
	var total time.Duration
	for s != "" {
		i := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == 0 {
			return 0, false
		}
		n, err := strconv.ParseUint(s[:i], 10, 32)
		if err != nil {
			return 0, false
		}
		s = s[i:]
		found := false
		for _, u := range durationUnits {
			if rest, ok := strings.CutPrefix(s, u.suffix); ok {
				d := time.Duration(n) * u.unit
				if d/u.unit != time.Duration(n) || total+d < total {
					return 0, false
				}
				total += d
				s = rest
				found = true
				break
			}
		}
		if !found {
			return 0, false
		}
	}
	return total, true
}

// formatDuration writes whole seconds as such, so that a range of
// 1-2147483647 seconds stays readable.
func formatDuration(d time.Duration) string {
	if d%time.Second == 0 {
		return fmt.Sprintf("%ds", d/time.Second)
	}
	return d.String()
}

// Duration consumes a duration in the range min to max: a plain number of
// seconds, or numbers with the units ms, s, m, h, d and w, as in 1h30m.
func (p *Parser) Duration(min, max time.Duration) (time.Duration, bool) {
	t := p.tok
	if _, ok := p.Word("duration"); !ok {
		return 0, false
	}
	d, ok := parseDuration(t.Text)
	if !ok {
		p.Errorf(t.Pos, "invalid duration %q", t.Text)
		return 0, false
	}
	if d < min || d > max {
		p.Errorf(t.Pos, "duration %q out of range (%s-%s)", t.Text, formatDuration(min), formatDuration(max))
		return 0, false
	}
	return d, true
}

// Bool consumes "yes" or "no". Grammars in the style of OpenBSD usually
// prefer a "no" prefix for negated options, written as p.Accept("no").
func (p *Parser) Bool() (bool, bool) {
	v, ok := p.Enum(`"yes" or "no"`, "yes", "no")
	return v == "yes", ok
}

// Enum consumes one of the given words. what describes the choice in the
// error message.
func (p *Parser) Enum(what string, values ...string) (string, bool) {
	t := p.tok
	if _, ok := p.Word(what); !ok {
		return "", false
	}
	for _, v := range values {
		if t.Text == v {
			return v, true
		}
	}
	p.Errorf(t.Pos, "expected %s, got %q", what, t.Text)
	return "", false
}

// Addr consumes an IPv4 or IPv6 address without zone.
func (p *Parser) Addr() (netip.Addr, bool) {
	t := p.tok
	if _, ok := p.Word("address"); !ok {
		return netip.Addr{}, false
	}
	a, err := netip.ParseAddr(t.Text)
	if err != nil || a.Zone() != "" {
		p.Errorf(t.Pos, "invalid address %q", t.Text)
		return netip.Addr{}, false
	}
	return a, true
}

// Prefix consumes a network in CIDR notation. Host bits must be zero.
func (p *Parser) Prefix() (netip.Prefix, bool) {
	t := p.tok
	if _, ok := p.Word("network"); !ok {
		return netip.Prefix{}, false
	}
	pfx, err := netip.ParsePrefix(t.Text)
	if err != nil {
		p.Errorf(t.Pos, "invalid network %q", t.Text)
		return netip.Prefix{}, false
	}
	if pfx != pfx.Masked() {
		p.Errorf(t.Pos, "network %s has host bits set, did you mean %s?", t.Text, pfx.Masked())
		return netip.Prefix{}, false
	}
	return pfx, true
}
