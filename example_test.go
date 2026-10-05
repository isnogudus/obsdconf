package obsdconf_test

import (
	"fmt"
	"time"

	"github.com/isnogudus/obsdconf"
)

func Example() {
	src := `
broker = "ssl://localhost:8883"

mqtt $broker {
	client id "e3dc-1"
	keepalive 30s
}
`
	var url, clientID string
	var keepalive time.Duration

	p := obsdconf.New("bridge.conf", []byte(src), obsdconf.Options{
		Keywords: []string{"mqtt", "client", "keepalive"},
	})
	err := p.Parse(func() bool {
		if !p.Expect("mqtt") {
			return false
		}
		var ok bool
		if url, ok = p.Text("broker URL"); !ok {
			return false
		}
		return p.Block("mqtt", func() bool {
			switch {
			case p.Accept("client", "id"):
				clientID, ok = p.Text("client id")
			case p.Accept("keepalive"):
				keepalive, ok = p.Duration(time.Second, time.Hour)
			default:
				p.Errorf(p.Tok().Pos, "unknown mqtt option %q", p.Tok().Text)
				ok = false
			}
			return ok
		})
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(url, clientID, keepalive)
	// Output: ssl://localhost:8883 e3dc-1 30s
}
