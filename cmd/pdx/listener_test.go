package main

import (
	"testing"
)

func TestListenWithReuseAddr_BindsFreePort(t *testing.T) {
	l, err := listenWithReuseAddr("127.0.0.1:0") // :0 = kernel picks free port
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()
	if l.Addr() == nil {
		t.Fatal("nil Addr")
	}
}

func TestListenWithReuseAddr_RebindsImmediately(t *testing.T) {
	// First listener, get addr
	l1, err := listenWithReuseAddr("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l1.Addr().String()
	if err := l1.Close(); err != nil {
		t.Fatal(err)
	}
	// Immediately rebind same addr — should succeed due to SO_REUSEADDR
	l2, err := listenWithReuseAddr(addr)
	if err != nil {
		t.Fatalf("rebind: %v", err)
	}
	l2.Close()
}
