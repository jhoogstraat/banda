// Package tunnel relays TCP connections over the runner's Connect stream: the
// API's preview proxy dials ports inside the sandbox through it. Both ends use
// a Mux; each connection is a net.Conn.
package tunnel

import (
	"errors"
	"io"
	"maps"
	"net"
	"slices"
	"sync"
	"time"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

const (
	chunk  = 32 << 10
	window = 8 // unacknowledged data messages per direction, see HttpTunnel
)

// Mux holds one side's connections. send carries messages to the other side.
type Mux struct {
	send  func(*bandav1.HttpTunnel) error
	mu    sync.Mutex
	conns map[string]*Conn
}

func NewMux(send func(*bandav1.HttpTunnel) error) *Mux {
	return &Mux{send: send, conns: map[string]*Conn{}}
}

// Open registers a connection. The side that dials sends the first message.
func (m *Mux) Open(id string) *Conn {
	c := &Conn{id: id, mux: m, in: make(chan []byte, window), credit: make(chan struct{}, window), done: make(chan struct{})}
	for range window {
		c.credit <- struct{}{}
	}
	m.mu.Lock()
	m.conns[id] = c
	m.mu.Unlock()
	return c
}

// Deliver hands a message from the other side to its connection. It never
// blocks, so the stream's receive loop can call it. Unknown ids are dropped.
func (m *Mux) Deliver(t *bandav1.HttpTunnel) {
	m.mu.Lock()
	c := m.conns[t.Id]
	m.mu.Unlock()
	if c == nil {
		return
	}
	if len(t.Data) > 0 {
		select {
		case c.in <- t.Data:
		default: // the peer ignored the window
			c.end(true, errors.New("tunnel overrun"))
			return
		}
	}
	for range t.Ack {
		select {
		case c.credit <- struct{}{}:
		default:
		}
	}
	if t.Close {
		var err error
		if t.Error != "" {
			err = errors.New(t.Error)
		}
		c.end(false, err)
	}
}

// CloseAll ends every connection, e.g. when the stream is gone.
func (m *Mux) CloseAll() {
	m.mu.Lock()
	conns := slices.Collect(maps.Values(m.conns))
	m.mu.Unlock()
	for _, c := range conns {
		c.end(false, errors.New("the environment went away"))
	}
}

// Conn is one relayed connection.
type Conn struct {
	id     string
	mux    *Mux
	in     chan []byte
	credit chan struct{}
	buf    []byte // the rest of the chunk being read

	once sync.Once
	done chan struct{}
	err  error // why it ended, if it failed; set before done is closed
}

func (c *Conn) Read(p []byte) (int, error) {
	if len(c.buf) == 0 {
		select {
		case c.buf = <-c.in:
		case <-c.done: // drain what arrived before the close
			select {
			case c.buf = <-c.in:
			default:
				if c.err != nil {
					return 0, c.err
				}
				return 0, io.EOF
			}
		}
		c.mux.send(&bandav1.HttpTunnel{Id: c.id, Ack: 1})
	}
	n := copy(p, c.buf)
	c.buf = c.buf[n:]
	return n, nil
}

func (c *Conn) Write(p []byte) (int, error) {
	n := 0
	for len(p) > 0 {
		select {
		case <-c.done:
			return n, net.ErrClosed
		case <-c.credit:
			if c.closed() {
				return n, net.ErrClosed
			}
		}
		b := p[:min(len(p), chunk)]
		if err := c.mux.send(&bandav1.HttpTunnel{Id: c.id, Data: slices.Clone(b)}); err != nil {
			c.end(false, err)
			return n, err
		}
		n, p = n+len(b), p[len(b):]
	}
	return n, nil
}

func (c *Conn) closed() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *Conn) Close() error {
	c.end(true, nil)
	return nil
}

// CloseWithError ends the connection and tells the other side why.
func (c *Conn) CloseWithError(err error) {
	c.end(true, err)
}

// end closes the connection once, telling the other side if notify is set.
func (c *Conn) end(notify bool, err error) {
	c.once.Do(func() {
		c.err = err
		close(c.done)
		c.mux.mu.Lock()
		delete(c.mux.conns, c.id)
		c.mux.mu.Unlock()
		if notify {
			msg := &bandav1.HttpTunnel{Id: c.id, Close: true}
			if err != nil {
				msg.Error = err.Error()
			}
			c.mux.send(msg)
		}
	})
}

// The rest of net.Conn, for http.Transport. Deadlines are not supported: the
// proxy relies on contexts and on the stream ending.
func (c *Conn) LocalAddr() net.Addr              { return addr{} }
func (c *Conn) RemoteAddr() net.Addr             { return addr{} }
func (c *Conn) SetDeadline(time.Time) error      { return nil }
func (c *Conn) SetReadDeadline(time.Time) error  { return nil }
func (c *Conn) SetWriteDeadline(time.Time) error { return nil }

type addr struct{}

func (addr) Network() string { return "tunnel" }
func (addr) String() string  { return "tunnel" }
