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
