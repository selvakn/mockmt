package mockmt

import (
	"context"
	"net"
	"sync"
	"time"
)

// limitedListener wraps a net.Listener, admitting at most maxConns
// simultaneous connections. A connection arriving over the cap is answered
// with a 421 greeting and closed immediately -- never queued in the accept
// backlog, where an over-cap agent would see a silent hang instead of a
// clear, retryable error (research R16).
//
// The SQLite driver in use has no incremental blob I/O, so a whole message
// is held in memory per connection; this cap plus MAX_MESSAGE_BYTES is what
// bounds process memory.
type limitedListener struct {
	net.Listener
	sem chan struct{}
}

func newLimitedListener(l net.Listener, maxConns int) *limitedListener {
	return &limitedListener{Listener: l, sem: make(chan struct{}, maxConns)}
}

func (l *limitedListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}

		select {
		case l.sem <- struct{}{}:
			return &limitedConn{Conn: c, release: l.release}, nil
		default:
			// RFC 5321 section 3.1 permits 421 as the greeting reply.
			// Rejecting here means the client never sees anything but a
			// standard, immediately retryable error.
			_, _ = c.Write([]byte("421 4.7.0 Too many concurrent connections\r\n"))
			_ = c.Close()
		}
	}
}

func (l *limitedListener) release() {
	select {
	case <-l.sem:
	default:
	}
}

// limitedConn releases its listener slot on Close, exactly once no matter
// how many times or from where Close is called. Tying release to Close
// (rather than to a backend callback like Session.Logout) matches the
// guarantee go-smtp actually makes: every accepted connection is closed.
type limitedConn struct {
	net.Conn
	release  func()
	released sync.Once
}

func (c *limitedConn) Close() error {
	c.released.Do(c.release)
	return c.Conn.Close()
}

// ioSemaphore bounds concurrent whole-message reads -- relay sends,
// attachment previews, attachment downloads -- since materializing a raw
// message is the memory-expensive operation the SQLite driver cannot avoid
// (research R16). A nil *ioSemaphore always grants immediately, so code
// that has not been wired to a running config (e.g. focused unit tests)
// does not need to special-case it.
type ioSemaphore struct {
	sem chan struct{}
}

func newIOSemaphore(n int) *ioSemaphore {
	if n <= 0 {
		n = 1
	}
	return &ioSemaphore{sem: make(chan struct{}, n)}
}

// ioSemaphoreAcquireTimeout bounds how long a caller waits for a slot
// before being told to retry (contracts/relay-api.md: 503 + Retry-After).
const ioSemaphoreAcquireTimeout = 5 * time.Second

// Acquire waits up to ioSemaphoreAcquireTimeout for a slot, or until ctx is
// done, and reports whether one was obtained.
func (s *ioSemaphore) Acquire(ctx context.Context) bool {
	if s == nil {
		return true
	}

	ctx, cancel := context.WithTimeout(ctx, ioSemaphoreAcquireTimeout)
	defer cancel()

	select {
	case s.sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// Release returns a slot acquired by Acquire. Safe to call on a nil
// *ioSemaphore.
func (s *ioSemaphore) Release() {
	if s == nil {
		return
	}
	select {
	case <-s.sem:
	default:
	}
}
