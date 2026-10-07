// internal/terminal/relay.go
package terminal

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// ResizeMsg is sent from the client to resize the PTY.
type ResizeMsg struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

type Relay struct {
	cmd     string
	args    []string
	cwd     string
	OnStart      func() // called after PTY starts, before I/O goroutines
	PingInterval time.Duration // default: 30s
	PongTimeout  time.Duration // default: 10s

	// WindowSize, when set, makes the relay poll the actual window size and
	// push it to the client as a text frame {"type":"window","cols":N,"rows":N}:
	// once at connection start and afterwards only when the value changes.
	// Query errors are skipped (nothing sent, connection kept). ctx is cancelled
	// when the connection ends, so a stuck query must honour it.
	WindowSize         func(ctx context.Context) (cols, rows uint16, err error)
	WindowPollInterval time.Duration // default: 1s
	// WindowQueryTimeout bounds each single WindowSize call (default 2s) so one
	// stuck tmux query cannot stall reporting for the rest of the connection.
	WindowQueryTimeout time.Duration

	// PTYSize, when set, slaves the PTY to the window: the PTY starts at the
	// returned size, the client's own resize messages are ignored, and every
	// poll tick re-applies the size if it changed. The caller returns window
	// size plus status rows: tmux sizes a window from even a lone ignore-size
	// client, so any other PTY size would shrink the desktop window. If the
	// size cannot be learned at start the connection is refused (1011) rather
	// than started at a guessed size.
	PTYSize func(ctx context.Context) (cols, rows uint16, err error)
}

// WindowMsg is sent to the client when the window's actual size is (re)reported.
type WindowMsg struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

func NewRelay(cmd string, args []string, cwd string) *Relay {
	return &Relay{cmd: cmd, args: args, cwd: cwd}
}

func (r *Relay) HandleWebSocket(w http.ResponseWriter, req *http.Request) {
	conn, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		log.Printf("websocket upgrade: %v", err)
		return
	}
	defer conn.Close()

	c := exec.Command(r.cmd, r.args...)
	c.Dir = r.cwd
	c.Env = append(os.Environ(), "TERM=xterm-256color")

	startSize := pty.Winsize{Cols: 80, Rows: 24}
	if r.PTYSize != nil {
		qctx, qcancel := context.WithTimeout(req.Context(), r.queryTimeout())
		cols, rows, err := r.PTYSize(qctx)
		qcancel()
		if err != nil {
			log.Printf("pty size: %v", err)
			_ = conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "window size unavailable"),
				time.Now().Add(time.Second))
			return
		}
		startSize = pty.Winsize{Cols: cols, Rows: rows}
	}

	ptmx, err := pty.StartWithSize(c, &startSize)
	if err != nil {
		log.Printf("pty start: %v", err)
		return
	}
	if r.OnStart != nil {
		r.OnStart()
	}
	defer func() {
		ptmx.Close()
		c.Wait()
	}()

	// Resolve ping/pong durations with defaults
	pingInterval := r.PingInterval
	if pingInterval == 0 {
		pingInterval = 30 * time.Second
	}
	pongTimeout := r.PongTimeout
	if pongTimeout == 0 {
		pongTimeout = 10 * time.Second
	}

	// Pong handling — reset read deadline on each pong received
	conn.SetReadDeadline(time.Now().Add(pingInterval + pongTimeout))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pingInterval + pongTimeout))
		return nil
	})

	var wg sync.WaitGroup
	// writeMu serialises every WriteMessage on conn (gorilla allows a single
	// writer): the batcher's binary frames and the window text frames.
	var writeMu sync.Mutex

	// ctx ends with the connection: either I/O goroutine exiting cancels it, so
	// a stuck window query is released even while the other goroutine is still
	// blocked in a PTY read.
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()

	// firstWindow is closed once the first window reading has been attempted
	// (sent, failed or timed out). The PTY→WS writer waits for it so the
	// client knows the window size before any terminal output arrives; it is
	// bounded by WindowQueryTimeout, so output is never held back for long.
	firstWindow := make(chan struct{})
	if r.WindowSize != nil {
		done := make(chan struct{})
		go func() {
			defer close(done)
			var once sync.Once
			ready := func() { once.Do(func() { close(firstWindow) }) }
			defer ready()
			curPTY := startSize // only this goroutine touches it
			applyPTY := func(cols, rows uint16) {
				if cols == curPTY.Cols && rows == curPTY.Rows {
					return
				}
				curPTY = pty.Winsize{Cols: cols, Rows: rows}
				pty.Setsize(ptmx, &curPTY)
			}
			r.pollWindowSize(ctx, ready, func(m WindowMsg) error {
				data, _ := json.Marshal(m)
				writeMu.Lock()
				err := conn.WriteMessage(websocket.TextMessage, data)
				writeMu.Unlock()
				return err
			}, applyPTY, ptmx)
		}()
		defer func() {
			cancel()
			<-done
		}()
	} else {
		close(firstWindow)
	}

	// Periodic ping to keep connection alive through proxies/firewalls.
	// WriteControl is documented as concurrent-safe with WriteMessage
	// (gorilla/websocket: "Close and WriteControl can be called concurrently
	// with all other methods"), so no writeMu needed here.
	// Not in WaitGroup — exits when conn.Close() causes WriteControl error.
	go func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		for range ticker.C {
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(pongTimeout)); err != nil {
				return
			}
		}
	}()

	// PTY → WebSocket (batched, mutex-protected writes)
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		defer conn.Close() // wake WS read goroutine on PTY EOF
		batcher := NewBatcher(16*time.Millisecond, 64*1024, func(data []byte) {
			writeMu.Lock()
			err := conn.WriteMessage(websocket.BinaryMessage, data)
			writeMu.Unlock()
			if err != nil {
				ptmx.Close() // signal PTY read to exit
			}
		})
		defer batcher.Stop()
		select {
		case <-firstWindow:
		case <-ctx.Done():
			return
		}
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				batcher.Write(buf[:n])
			}
			if err != nil {
				if err != io.EOF {
					log.Printf("pty read: %v", err)
				}
				return
			}
		}
	}()

	// WebSocket → PTY (with resize handling)
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		defer conn.Close() // wake PTY→WS goroutine on read-deadline/disconnect
		defer ptmx.Close() // wake PTY read goroutine on WS disconnect
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			// Check if it's a resize message
			var resize ResizeMsg
			if json.Unmarshal(msg, &resize) == nil && resize.Type == "resize" {
				if r.PTYSize == nil { // a slaved PTY follows the window, not the client
					pty.Setsize(ptmx, &pty.Winsize{Cols: resize.Cols, Rows: resize.Rows})
				}
				continue
			}
			// Regular input
			ptmx.Write(msg)
		}
	}()

	wg.Wait()
}

// queryTimeout bounds each single window/PTY size query (default 2s).
func (r *Relay) queryTimeout() time.Duration {
	if r.WindowQueryTimeout > 0 {
		return r.WindowQueryTimeout
	}
	return 2 * time.Second
}

// pollWindowSize reports the window size through send: always the first
// successful reading, then only changes. A failed send closes ptmx so both
// I/O goroutines wake up and the connection ends, like a failed batcher write.
// ready is called once the first reading has been attempted.
//
// With PTYSize set, applyPTY is called on every tick (before the frame, and
// even when the window size is unchanged — the status bar may have changed)
// with the size the PTY must have; it is the caller's job to skip no-ops.
func (r *Relay) pollWindowSize(ctx context.Context, ready func(), send func(WindowMsg) error, applyPTY func(cols, rows uint16), ptmx io.Closer) {
	interval := r.WindowPollInterval
	if interval <= 0 {
		interval = time.Second
	}
	var last WindowMsg
	have := false
	tick := func() bool {
		qctx, qcancel := context.WithTimeout(ctx, r.queryTimeout())
		cols, rows, err := r.WindowSize(qctx)
		var pcols, prows uint16
		var perr error
		if err == nil && r.PTYSize != nil {
			pcols, prows, perr = r.PTYSize(qctx)
		}
		qcancel()
		if err != nil || ctx.Err() != nil {
			return true
		}
		if r.PTYSize != nil && perr == nil && applyPTY != nil {
			applyPTY(pcols, prows)
		}
		if have && last.Cols == cols && last.Rows == rows {
			return true
		}
		m := WindowMsg{Type: "window", Cols: cols, Rows: rows}
		if err := send(m); err != nil {
			ptmx.Close()
			return false
		}
		last, have = m, true
		return true
	}
	ok := tick()
	ready()
	if !ok {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !tick() {
				return
			}
		}
	}
}
