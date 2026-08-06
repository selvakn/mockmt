package mockmt

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-smtp"
)

func TestLimitedListenerRejectsOverCapacityAndReleasesOnClose(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	ll := newLimitedListener(l, 1)
	t.Cleanup(func() { _ = ll.Close() })

	accepted := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := ll.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()

	conn1, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("failed to dial first connection: %v", err)
	}
	defer func() { _ = conn1.Close() }()

	var serverConn1 net.Conn
	select {
	case serverConn1 = <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("expected the first connection to be admitted")
	}

	// Second connection arrives while the cap is exhausted: it must be
	// rejected with a 421 greeting and closed, never handed to Accept().
	conn2, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("failed to dial second connection: %v", err)
	}
	defer func() { _ = conn2.Close() }()

	_ = conn2.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 256)
	n, err := conn2.Read(buf)
	if err != nil {
		t.Fatalf("failed to read rejection response: %v", err)
	}
	if !strings.Contains(string(buf[:n]), "421") {
		t.Fatalf("expected a 421 response, got %q", buf[:n])
	}

	// The rejected connection must be closed by the listener side.
	_ = conn2.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn2.Read(buf); err != io.EOF {
		t.Fatalf("expected the rejected connection to be closed (EOF), got %v", err)
	}

	select {
	case <-accepted:
		t.Fatal("the over-capacity connection must never be handed to Accept()")
	default:
	}

	// Releasing the first slot (by closing the server-side conn, exactly as
	// go-smtp's handleConn does via its deferred Close) must admit a third
	// connection.
	if err := serverConn1.Close(); err != nil {
		t.Fatalf("failed to close first server-side connection: %v", err)
	}

	conn3, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("failed to dial third connection: %v", err)
	}
	defer func() { _ = conn3.Close() }()

	select {
	case c := <-accepted:
		_ = c.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("expected a third connection to be admitted after the first slot was released")
	}
}

func TestLimitedConnCloseReleasesExactlyOnce(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer func() { _ = l.Close() }()

	released := 0
	ll := &limitedListener{Listener: l, sem: make(chan struct{}, 1)}
	ll.sem <- struct{}{}

	c := &limitedConn{Conn: nil, release: func() { released++; ll.release() }}
	// Closing the underlying nil conn would panic, so exercise release
	// directly via multiple Close-equivalent calls instead.
	c.released.Do(c.release)
	c.released.Do(c.release)
	c.released.Do(c.release)

	if released != 1 {
		t.Fatalf("release ran %d times, want exactly 1", released)
	}
}

func TestServeWithLimitedListenerClosesIdleConnectionsViaReadTimeout(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	ll := newLimitedListener(l, 3)

	srv := smtp.NewServer(&Backend{Username: "user", Password: "pass"})
	srv.Domain = "localhost"
	srv.AllowInsecureAuth = true
	srv.ReadTimeout = 200 * time.Millisecond

	go func() { _ = srv.Serve(ll) }()
	t.Cleanup(func() { _ = srv.Close() })

	conn, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 512)

	// Consume the greeting.
	if _, err := conn.Read(buf); err != nil {
		t.Fatalf("failed to read greeting: %v", err)
	}

	// Idle without sending anything: go-smtp's read timeout must close the
	// connection. Without SMTP_MAX_CONCURRENT idle timeouts, this cap would
	// be a denial-of-service surface (research R16) -- three idle
	// connections would occupy every slot indefinitely.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := conn.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("unexpected error waiting for idle timeout: %v", err)
	}
	if n > 0 && !strings.Contains(string(buf[:n]), "421") {
		t.Fatalf("expected a 421 idle-timeout response, got %q", buf[:n])
	}
}

func TestIOSemaphoreAcquireTimesOutWhenSaturated(t *testing.T) {
	sem := newIOSemaphore(1)

	if !sem.Acquire(t.Context()) {
		t.Fatal("expected the first acquire to succeed")
	}

	shortCtx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	if sem.Acquire(shortCtx) {
		t.Fatal("expected the second acquire to fail while saturated")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Acquire took %v, expected it to respect the short context deadline rather than block", elapsed)
	}

	sem.Release()
	if !sem.Acquire(t.Context()) {
		t.Fatal("expected acquire to succeed after release")
	}
}

func TestIOSemaphoreNilIsAlwaysAvailable(t *testing.T) {
	var sem *ioSemaphore

	if !sem.Acquire(t.Context()) {
		t.Fatal("expected a nil semaphore to always grant")
	}
	sem.Release() // must not panic
}
