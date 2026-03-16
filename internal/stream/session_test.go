// internal/stream/session_test.go
package stream_test

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/tmux-box/internal/stream"
)

func TestSessionStartStop(t *testing.T) {
	// Use "cat" as a fake claude -p (echoes stdin to stdout)
	s, err := stream.NewSession("cat", []string{}, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	if !s.Running() {
		t.Error("session should be running")
	}

	s.Stop()

	if s.Running() {
		t.Error("session should not be running after Stop")
	}
}

func TestSessionSendReceive(t *testing.T) {
	s, err := stream.NewSession("cat", []string{}, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	// Subscribe to output
	ch := s.Subscribe()
	defer s.Unsubscribe(ch)

	// Send a JSON line
	msg := map[string]string{"type": "user", "text": "hello"}
	data, _ := json.Marshal(msg)
	s.Send(data)

	// Should receive it back (cat echoes)
	select {
	case line := <-ch:
		if !strings.Contains(string(line), "hello") {
			t.Errorf("want line containing hello, got %s", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for echo")
	}
}

func TestSessionMultipleSubscribers(t *testing.T) {
	s, err := stream.NewSession("cat", []string{}, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	ch1 := s.Subscribe()
	ch2 := s.Subscribe()
	defer s.Unsubscribe(ch1)
	defer s.Unsubscribe(ch2)

	msg, _ := json.Marshal(map[string]string{"test": "multi"})
	s.Send(msg)

	var wg sync.WaitGroup
	wg.Add(2)
	for _, ch := range []<-chan []byte{ch1, ch2} {
		go func(c <-chan []byte) {
			defer wg.Done()
			select {
			case <-c:
			case <-time.After(2 * time.Second):
				t.Error("timeout")
			}
		}(ch)
	}
	wg.Wait()
}
