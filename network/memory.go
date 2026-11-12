package network

import (
	"net"
	"os"
	"sync"
	"time"
)

// memoryQueue is how many batches each direction of a memory connection holds before a
// write waits: enough that a server never waits on a client that reads promptly.
const memoryQueue = 4096

// Memory is a transport inside the process: Dial makes a connection whose other end the
// server accepts. For load tests (package sim) and tests: bots join with no sockets.
type Memory struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

// NewMemory makes a memory transport. Give it to the server, then Dial it.
func NewMemory() *Memory {
	return &Memory{conns: make(chan net.Conn), closed: make(chan struct{})}
}

// Accept waits for the next Dial.
func (t *Memory) Accept() (net.Conn, error) {
	select {
	case c := <-t.conns:
		return c, nil
	case <-t.closed:
		return nil, net.ErrClosed
	}
}

// Addr is an empty UDP address.
func (t *Memory) Addr() net.Addr { return &net.UDPAddr{} }

// SetPong does nothing: nobody pings a memory transport.
func (t *Memory) SetPong([]byte) {}

// Close stops Accept and Dial.
func (t *Memory) Close() error { t.once.Do(func() { close(t.closed) }); return nil }

// Dial connects to the server and returns the client's end, which reads and writes whole
// batches (conn.Transport). It fails once the transport is closed.
func (t *Memory) Dial() (*MemoryConn, error) {
	client, server := memoryPair()
	select {
	case t.conns <- server:
		return client, nil
	case <-t.closed:
		return nil, net.ErrClosed
	}
}

// MemoryConn is one end of a memory connection. Each write is one batch.
type MemoryConn struct {
	in, out  chan []byte
	mu       sync.Mutex
	deadline chan struct{}
	closed   chan struct{}
	once     *sync.Once
}

// memoryPair makes the two ends of a memory connection.
func memoryPair() (*MemoryConn, *MemoryConn) {
	a, b := make(chan []byte, memoryQueue), make(chan []byte, memoryQueue)
	closed, once := make(chan struct{}), new(sync.Once)
	return &MemoryConn{in: a, out: b, deadline: make(chan struct{}), closed: closed, once: once},
		&MemoryConn{in: b, out: a, deadline: make(chan struct{}), closed: closed, once: once}
}

// ReadPacket reads the next batch.
func (m *MemoryConn) ReadPacket() ([]byte, error) {
	m.mu.Lock()
	d := m.deadline
	m.mu.Unlock()
	// Batches sent before the close are read first, as a real connection delivers them:
	// select alone picks at random when both are ready.
	select {
	case b := <-m.in:
		return b, nil
	default:
	}
	select {
	case b := <-m.in:
		return b, nil
	case <-d:
		return nil, os.ErrDeadlineExceeded
	case <-m.closed:
		return nil, net.ErrClosed
	}
}

// Read reads the next batch into b.
func (m *MemoryConn) Read(b []byte) (int, error) {
	p, err := m.ReadPacket()
	return copy(b, p), err
}

// Write sends b as one batch.
func (m *MemoryConn) Write(b []byte) (int, error) {
	select {
	case m.out <- append([]byte(nil), b...):
		return len(b), nil
	case <-m.closed:
		return 0, net.ErrClosed
	}
}

// SetReadDeadline makes reads fail once t passes; zero clears it.
func (m *MemoryConn) SetReadDeadline(t time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t.IsZero() {
		m.deadline = make(chan struct{})
		return nil
	}
	d := m.deadline
	time.AfterFunc(time.Until(t), func() {
		m.mu.Lock()
		if m.deadline == d {
			close(d)
			m.deadline = make(chan struct{})
		}
		m.mu.Unlock()
	})
	return nil
}

// Close closes both ends.
func (m *MemoryConn) Close() error { m.once.Do(func() { close(m.closed) }); return nil }

// LocalAddr is an empty UDP address.
func (m *MemoryConn) LocalAddr() net.Addr { return &net.UDPAddr{} }

// RemoteAddr is a loopback UDP address.
func (m *MemoryConn) RemoteAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
}

// SetDeadline does nothing.
func (m *MemoryConn) SetDeadline(time.Time) error { return nil }

// SetWriteDeadline does nothing.
func (m *MemoryConn) SetWriteDeadline(time.Time) error { return nil }
