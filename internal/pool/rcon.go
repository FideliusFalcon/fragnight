package pool

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/gorcon/rcon"
)

// RCON runs a console command on a CS2 server.
type RCON interface {
	Exec(ctx context.Context, s Server, cmd string) (string, error)
}

type realRCON struct{ timeout time.Duration }

// NewRCON returns an RCON client with a 5 second timeout per command.
func NewRCON() RCON { return realRCON{timeout: 5 * time.Second} }

func (r realRCON) Exec(ctx context.Context, s Server, cmd string) (string, error) {
	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		conn, err := rcon.Dial(addr, s.RCONPassword, rcon.SetDialTimeout(r.timeout), rcon.SetDeadline(r.timeout))
		if err != nil {
			done <- result{err: fmt.Errorf("rcon %s: %w", addr, err)}
			return
		}
		defer conn.Close()
		out, err := conn.Execute(cmd)
		done <- result{out, err}
	}()
	select {
	case res := <-done:
		return res.out, res.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
