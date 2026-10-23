package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
)

// peekWait bounds Peek: connecting, the hello and the snapshot.
const peekWait = 2 * time.Second

// Peek reads the snapshot of the run served for the project at root and
// leaves at once, for `igris status` (SPEC §14): read-only, it says hello
// as name and sends no command. The error wraps ErrNotServed when no run
// answers.
func Peek(ctx context.Context, root, name string) (Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, peekWait)
	defer cancel()
	conn, err := Dial(ctx, root)
	if err != nil {
		return Snapshot{}, err
	}
	defer func() { _ = conn.Close() }()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if err := write(conn, Request{Type: TypeHello, Client: name}); err != nil {
		return Snapshot{}, fmt.Errorf("say hello to the run: %w", err)
	}
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64<<10), MaxLine+1)
	if !sc.Scan() {
		return Snapshot{}, errors.Join(errors.New("the connection closed before the snapshot"), sc.Err())
	}
	var snap Snapshot
	if err := json.Unmarshal(sc.Bytes(), &snap); err != nil || snap.Type != TypeSnapshot {
		return Snapshot{}, errors.New("the run answered hello with something other than a snapshot")
	}
	return snap, nil
}

// QuestionKind is a question's short label: "commit?", "skip?", "session
// lost", as the web UI and `igris status` show it.
func QuestionKind(q string) string { return questionKind(engine.Question(q)) }
