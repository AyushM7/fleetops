package main

import (
	"net"
	"testing"
	"time"
)

func TestListenerBoundsConnectionsAndRecoversCapacity(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	listener := &limitedListener{Listener: base, limit: 1}
	client, err := net.Dial("tcp", base.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	first, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan net.Conn, 1)
	go func() { next, _ := listener.Accept(); accepted <- next }()
	blocked, err := net.Dial("tcp", base.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer blocked.Close()
	blocked.SetReadDeadline(time.Now().Add(time.Second))
	var buffer [1]byte
	if _, err := blocked.Read(buffer[:]); err == nil {
		t.Fatal("connection overflow not closed")
	}
	first.Close()
	first.Close()
	if listener.active.Load() != 0 {
		t.Fatal("double close released more than one slot")
	}
	next, err := net.Dial("tcp", base.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	select {
	case connection := <-accepted:
		if connection == nil {
			t.Fatal("listener failed after overload")
		}
		connection.Close()
	case <-time.After(time.Second):
		t.Fatal("connection capacity did not recover")
	}
}
