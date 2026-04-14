// internal/terminal/relay.go
package terminal

import (
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

	ptmx, err := pty.StartWithSize(c, &pty.Winsize{Cols: 80, Rows: 24})
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
	var writeMu sync.Mutex

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
				pty.Setsize(ptmx, &pty.Winsize{Cols: resize.Cols, Rows: resize.Rows})
				continue
			}
			// Regular input
			ptmx.Write(msg)
		}
	}()

	wg.Wait()
}
