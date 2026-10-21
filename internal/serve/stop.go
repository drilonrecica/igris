package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// ErrLost is returned by Stop when the connection ends before the run has
// stopped.
var ErrLost = errors.New("the connection to the run was lost before it stopped")

// stopID is the id of the stop command Stop sends.
const stopID = "stop"

// Stop asks the run served on conn to stop, as the run view's Stop run
// does (the sessions stay open), and waits until it has: the run's
// run_stopped event, or a snapshot of a run that already ended (SPEC §14,
// igris stop). client names this igris in the hello, e.g. "igris 1.4.0".
// It closes conn.
func Stop(ctx context.Context, conn net.Conn, client string) error {
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	send := func(r Request) error {
		r.V = Protocol
		b, err := encode(r)
		if err == nil {
			_, err = conn.Write(b)
		}
		return err
	}
	lost := func(err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return fmt.Errorf("%w: %w", ErrLost, err)
		}
		return ErrLost
	}
	if err := send(Request{Type: TypeHello, Client: client}); err != nil {
		return lost(err)
	}
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64<<10), MaxLine+1)
	asked := false
	for sc.Scan() {
		var m struct {
			Type  string `json:"type"`
			ID    string `json:"id"`
			Error string `json:"error"`
			Ended bool   `json:"ended"`
		}
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			return fmt.Errorf("the run sent something that is not a protocol message: %w", err)
		}
		switch {
		case m.Type == TypeError && (m.ID == "" || m.ID == stopID):
			return fmt.Errorf("the run refused the stop: %s", textsafe.Line(m.Error))
		case m.Type == string(engine.RunStopped), m.Type == TypeSnapshot && m.Ended:
			return nil
		case m.Type == TypeSnapshot && !asked:
			asked = true
			if err := send(Request{Type: CmdStop, ID: stopID}); err != nil {
				return lost(err)
			}
		}
	}
	return lost(sc.Err())
}
