// Package obsdconf parses configuration files in the style of OpenBSD
// daemons such as pf.conf(5), httpd.conf(5) and relayd.conf(5): one
// statement per line, keywords instead of punctuation, curly braces for
// blocks and lists, macros and include.
//
// The package handles everything that is the same in every such file and
// leaves the grammar to the caller, who writes it as plain recursive
// descent with the helpers of Parser:
//
//   - Lexing: words, quoted strings, # comments, \ at the end of a line.
//   - Macros: name = value at top level, used as $name. As in pf.conf, a
//     quoted value is split into tokens again, so lan = "{ a b }" defines
//     a list. Options.Macros predefines macros, like pfctl -D.
//   - Macro references inside words: as in yylex() of OpenBSD's parse.y,
//     the name ends at the first character that is not a letter, digit or
//     underscore, and the rest of the word is kept, so with
//     movies = "/export/movies" the word $movies/plex is
//     /export/movies/plex. OpenBSD cannot do this for $x/y, since / ends
//     an unquoted word there; obsdconf allows / in words. ${name} delimits
//     the name, as in the shell, so that a letter, digit or underscore may
//     follow it, as in ${base}_old; OpenBSD has no such form. A word may hold
//     several references, as in $host:$port, and text before them. Each
//     must expand to a single word: $lan/x is an error if lan is a list,
//     while $lan alone still stands for the list. In an unquoted word
//     every $ starts a reference and a $ without a name is an error, so
//     values with a literal $ must be quoted. Quoted strings are never
//     expanded.
//   - include "file", relative to the including file, with loop detection.
//   - Lists ({ a b }, { a, b }, spanning lines) and blocks ({ on its own
//     line ... }).
//   - Values: Word, Text, Number, Port, Duration, Bool, Enum, Addr, Prefix.
//   - Multi-word keywords with Accept("client", "id"); negated options with
//     Accept("no").
//   - UnusedMacros reports macros that are defined but never used.
//   - Errors as file:line: message. After an error the parser skips to the
//     end of the statement and goes on, so one run reports all errors.
//   - Options.Secret checks file permissions like check_file_secrecy() for
//     configurations with passwords.
package obsdconf
