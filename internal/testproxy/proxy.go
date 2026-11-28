// Package testproxy is a small TCP forwarder for tests: it sits between a client and a server and can add a delay to
// every chunk, cut the open connections and refuse new ones. Its listener is open from the start.
package testproxy

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	dialTimeout = 2 * time.Second
	chunkSize   = 32 * 1024
)

// Proxy forwards the connections that reach Addr to the target.
type Proxy struct {
	listener net.Listener
	conns    map[net.Conn]struct{}
	done     chan struct{}
	target   string
	wg       sync.WaitGroup
	mu       sync.Mutex
	delay    atomic.Int64
	refuse   bool
	closed   bool
}

// Start listens on a free loopback port and forwards to target.
func Start(target string) (*Proxy, error) {
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	p := &Proxy{listener: listener, conns: map[net.Conn]struct{}{}, done: make(chan struct{})}

	p.target = target
	p.wg.Go(p.accept)

	return p, nil
}

// SetTarget changes the address that new connections are forwarded to (a server that came back on another port).
func (p *Proxy) SetTarget(target string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.target = target
}

// Addr is the address clients dial.
func (p *Proxy) Addr() string { return p.listener.Addr().String() }

// SetDelay adds d to every chunk that is forwarded, in both directions (zero: no delay). It applies to the open
// connections too, from the next chunk.
func (p *Proxy) SetDelay(d time.Duration) { p.delay.Store(int64(d)) }

// Cut closes every open connection. New connections are still accepted.
func (p *Proxy) Cut() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for conn := range p.conns {
		_ = conn.Close()
	}

	clear(p.conns)
}

// SetRefuse makes the proxy close every new connection at once while refuse is true, and cuts the open ones: the
// target went away and comes back with SetRefuse(false).
func (p *Proxy) SetRefuse(refuse bool) {
	p.mu.Lock()
	p.refuse = refuse
	p.mu.Unlock()

	if refuse {
		p.Cut()
	}
}

// Close stops the listener, closes every connection and waits for the goroutines.
func (p *Proxy) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()

		return
	}

	p.closed = true
	close(p.done)
	p.mu.Unlock()

	_ = p.listener.Close()

	p.Cut()
	p.wg.Wait()
}

func (p *Proxy) currentTarget() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.target
}

func (p *Proxy) refusing() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.refuse || p.closed
}

// untrack forgets the connections that a pump closed.
func (p *Proxy) untrack(conns ...net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, conn := range conns {
		delete(p.conns, conn)
	}
}

// track registers a connection, and reports false when the proxy refuses or is closed.
func (p *Proxy) track(conns ...net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.refuse || p.closed {
		return false
	}

	for _, conn := range conns {
		p.conns[conn] = struct{}{}
	}

	return true
}

func (p *Proxy) accept() {
	for {
		client, err := p.listener.Accept()
		if err != nil {
			return
		}

		if p.refusing() {
			_ = client.Close()

			continue
		}

		upstream, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(context.Background(), "tcp", p.currentTarget())
		if err != nil || !p.track(client, upstream) {
			_ = client.Close()

			if upstream != nil {
				_ = upstream.Close()
			}

			continue
		}

		p.wg.Go(func() { p.pump(upstream, client) })
		p.wg.Go(func() { p.pump(client, upstream) })
	}
}

// pump copies src to dst chunk by chunk, holding each chunk for the current delay, and closes both ends at the end.
func (p *Proxy) pump(dst, src net.Conn) {
	defer func() {
		_ = dst.Close()
		_ = src.Close()

		p.untrack(dst, src)
	}()

	buf := make([]byte, chunkSize)

	for {
		read, err := src.Read(buf)
		if read > 0 {
			if wait := time.Duration(p.delay.Load()); wait > 0 {
				select {
				case <-time.After(wait):
				case <-p.done:
					return
				}
			}

			_, werr := dst.Write(buf[:read])
			if werr != nil {
				return
			}
		}

		if err != nil {
			return
		}
	}
}
