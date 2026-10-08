# obsdconf

A Go package for configuration files in the style of OpenBSD daemons
(`pf.conf`, `httpd.conf`, `relayd.conf`):

```
include "/etc/bridge.secrets"           # defines $mqtt_password

broker = "ssl://localhost:8883"

log level info

mqtt $broker {
	client id "e3dc-1"
	username e3dc
	password $mqtt_password
	keepalive 30s
}

no sandbox
```

obsdconf does the parts that are the same in every such file — lexing,
macros, `include`, lists, blocks, values, error reporting and recovery —
and leaves the grammar to you. You write it as plain recursive descent:

```go
p := obsdconf.New("bridge.conf", src, obsdconf.Options{
	Keywords: []string{"mqtt", "client", "keepalive"},
})
err := p.Parse(func() bool {
	if !p.Expect("mqtt") {
		return false
	}
	url, ok := p.Text("broker URL")
	if !ok {
		return false
	}
	return p.Block("mqtt", func() bool {
		switch {
		case p.Accept("client", "id"):
			cfg.ClientID, ok = p.Text("client id")
		case p.Accept("keepalive"):
			cfg.Keepalive, ok = p.Duration(time.Second, time.Hour)
		default:
			p.Errorf(p.Tok().Pos, "unknown mqtt option %q", p.Tok().Text)
			ok = false
		}
		return ok
	})
})
```

A statement function is called with the first word of a statement as the
current token. It consumes the statement and returns `false` after
reporting an error. The parser then skips to the end of the statement and
goes on, so one run reports every error:

```
bridge.conf:4: expected log level, got "loud"
bridge.conf:9: duration "2h" out of range (1s-3600s)
```

See the [package documentation](https://pkg.go.dev/github.com/isnogudus/obsdconf)
for the full API.

## Features

- Words, quoted strings (`\"` and `\\` escapes, UTF-8), `#` comments,
  `\` continuation lines.
- Macros: `name = value` at top level, used as `$name`. A quoted value is
  split into tokens again, as in `pf.conf`, so `lan = "{ a b }"` defines a
  list. `Options.Macros` predefines macros, like `pfctl -D`.
- `include "file"`, relative to the including file, with loop detection.
- Lists `{ a b }` (commas optional, may span lines) and blocks.
- Values: `Word`, `Text`, `Number`, `Port`, `Duration` (`500ms`, `90`,
  `1h30m`, `2w`), `Bool`, `Enum`, `Addr`, `Prefix`.
- Multi-word keywords: `p.Accept("client", "id")`; negated options:
  `p.Accept("no")`.
- `UnusedMacros` lists the macros that are defined but never used, which
  `pfctl` warns about since they are often typing errors.
- `Options.Secret` rejects files that are not owned by root or the current
  user, or that are group writable or accessible by others — the rules of
  `check_file_secrecy()` in OpenBSD daemons. It applies to included files
  too, so passwords can live in a separate file with mode 0600.

## Users

- [zonefile-go](https://github.com/isnogudus/zonefile-go)

The API is not stable before v1.0.0. It is meant to grow with the
projects that use it; planned users are
[e3dc-mqtt](https://github.com/isnogudus/e3dc-mqtt),
[mygekko-mqtt](https://github.com/isnogudus/mygekko-mqtt) and
[weft](https://github.com/isnogudus/weft). Their configurations need, among
other things, sub-second durations, multi-word keywords, secrets in
included files, repeated blocks with arguments (`attribute ou { … }`),
UTF-8 labels and `listen on ADDRESS port N`.

## License

MIT, see [LICENSE](LICENSE).
