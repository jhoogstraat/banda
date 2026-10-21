package tunnel

import (
	"bytes"
	"crypto/rand"
	"io"
	"testing"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

// pair wires two muxes together like the runner stream does: each side's
// messages are delivered in order by one goroutine.
func pair() (a, b *Mux) {
	ab, ba := make(chan *bandav1.HttpTunnel, 1024), make(chan *bandav1.HttpTunnel, 1024)
	a = NewMux(func(t *bandav1.HttpTunnel) error { ab <- t; return nil })
	b = NewMux(func(t *bandav1.HttpTunnel) error { ba <- t; return nil })
	go func() {
		for t := range ab {
			b.Deliver(t)
		}
	}()
	go func() {
		for t := range ba {
			a.Deliver(t)
		}
	}()
	return a, b
}

func TestEcho(t *testing.T) {
	a, b := pair()
	ca, cb := a.Open("x"), b.Open("x")
	go func() { io.Copy(cb, cb); cb.Close() }() // echo until a closes
	want := make([]byte, 50*chunk+123)          // far more than the window
	rand.Read(want)
	go func() { ca.Write(want) }()
	got := make([]byte, len(want))
	if _, err := io.ReadFull(ca, got); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("echo: %v, equal %v", err, bytes.Equal(got, want))
	}
	ca.Close()
	if _, err := ca.Write([]byte("x")); err == nil {
		t.Fatal("write after close")
	}
}

func TestCloseWithError(t *testing.T) {
	a, b := pair()
	ca, cb := a.Open("x"), b.Open("x")
	cb.Write([]byte("partial"))
	cb.CloseWithError(io.ErrUnexpectedEOF)
	got, err := io.ReadAll(ca)
	if string(got) != "partial" || err == nil || err.Error() != io.ErrUnexpectedEOF.Error() {
		t.Fatalf("got %q, %v", got, err)
	}
	a.Open("y")
	a.CloseAll()
	if len(a.conns) != 0 {
		t.Fatal("CloseAll left connections")
	}
}
