package obsdconf

import (
	"reflect"
	"testing"
)

func TestLexer(t *testing.T) {
	type tk struct {
		kind Kind
		text string
		line int
	}
	tests := []struct {
		name string
		src  string
		want []tk
	}{
		{
			name: "statement with list and comment",
			src:  "host a { 1.2.3.4, ::1 } # comment\n",
			want: []tk{
				{Word, "host", 1}, {Word, "a", 1}, {LBrace, "{", 1},
				{Word, "1.2.3.4", 1}, {Comma, ",", 1}, {Word, "::1", 1},
				{RBrace, "}", 1}, {Newline, "", 1}, {Newline, "", 2}, {EOF, "", 2},
			},
		},
		{
			name: "continuation and missing final newline",
			src:  "a \\\nb",
			want: []tk{{Word, "a", 1}, {Word, "b", 2}, {Newline, "", 2}, {EOF, "", 2}},
		},
		{
			name: "string with escapes",
			src:  `x = "say \"hi\" \\"`,
			want: []tk{
				{Word, "x", 1}, {Equals, "=", 1}, {String, `say "hi" \`, 1},
				{Newline, "", 1}, {EOF, "", 1},
			},
		},
		{
			name: "UTF-8 in strings and words",
			src:  `label de "Landes- / Bundesbehörde" Größe`,
			want: []tk{
				{Word, "label", 1}, {Word, "de", 1}, {String, "Landes- / Bundesbehörde", 1},
				{Word, "Größe", 1}, {Newline, "", 1}, {EOF, "", 1},
			},
		},
		{
			name: "unterminated string",
			src:  "\"abc\nx",
			want: []tk{
				{Illegal, "unterminated string", 1}, {Newline, "", 1},
				{Word, "x", 2}, {Newline, "", 2}, {EOF, "", 2},
			},
		},
		{
			name: "stray backslash",
			src:  `a\b`,
			want: []tk{
				{Word, "a", 1}, {Illegal, `"\" is only allowed at the end of a line`, 1},
				{Word, "b", 1}, {Newline, "", 1}, {EOF, "", 1},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newLexer("t", []byte(tt.src))
			var got []tk
			for {
				tok := l.next()
				got = append(got, tk{tok.Kind, tok.Text, tok.Pos.Line})
				if tok.Kind == EOF {
					break
				}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got  %v\nwant %v", got, tt.want)
			}
		})
	}
}
