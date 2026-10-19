package main

import (
	"net"
	"net/http"
	"sync"
	"sync/atomic"
)

type limitedListener struct {
	net.Listener
	active atomic.Int32
	limit  int32
}
type limitedConnection struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitedConnection) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

func (l *limitedListener) Accept() (net.Conn, error) {
	for {
		connection, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if l.active.Add(1) > l.limit {
			l.active.Add(-1)
			connection.Close()
			continue
		}
		return &limitedConnection{Conn: connection, release: func() { l.active.Add(-1) }}, nil
	}
}

type boundedServer struct{ *http.Server }

func (s boundedServer) ListenAndServe() error {
	listener, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return err
	}
	return s.Server.Serve(&limitedListener{Listener: listener, limit: 64})
}
