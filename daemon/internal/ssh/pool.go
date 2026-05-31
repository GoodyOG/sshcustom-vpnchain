package ssh

import (
	"context"
	"sync"
	"sync/atomic"
)

// Pool manages a set of SSH client connections to the same server.
// It provides round-robin selection among healthy connections.
type Pool struct {
	clients []*Client
	size    int
	mu      sync.Mutex
	idx     uint32
}

// NewPool creates a Pool with the given target size.
func NewPool(size int) *Pool {
	if size < 1 {
		size = 1
	}
	return &Pool{
		clients: make([]*Client, 0, size),
		size:    size,
	}
}

// Dial establishes N SSH connections using the same ConnectConfig.
// It stops on the first error after at least one successful connection,
// or returns the error if no connection could be established.
func (p *Pool) Dial(ctx context.Context, cfg ConnectConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Close any existing connections
	for _, c := range p.clients {
		if c != nil {
			c.Close()
		}
	}
	p.clients = make([]*Client, 0, p.size)

	for i := 0; i < p.size; i++ {
		c, err := Dial(ctx, cfg)
		if err != nil {
			if len(p.clients) == 0 {
				return err
			}
			// At least one connection succeeded; stop here
			break
		}
		p.clients = append(p.clients, c)
	}
	return nil
}

// Pick returns the next healthy client using round-robin selection.
// Returns nil if no healthy client is available.
func (p *Pool) Pick() *Client {
	p.mu.Lock()
	n := len(p.clients)
	if n == 0 {
		p.mu.Unlock()
		return nil
	}
	clients := make([]*Client, n)
	copy(clients, p.clients)
	p.mu.Unlock()

	start := atomic.AddUint32(&p.idx, 1)
	for i := 0; i < n; i++ {
		c := clients[(int(start)+i)%n]
		if c != nil {
			return c
		}
	}
	return nil
}

// CloseAll closes all connections in the pool and clears the list.
func (p *Pool) CloseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.clients {
		if c != nil {
			c.Close()
		}
	}
	p.clients = p.clients[:0]
}

// Healthy returns the number of non-nil clients in the pool.
func (p *Pool) Healthy() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	count := 0
	for _, c := range p.clients {
		if c != nil {
			count++
		}
	}
	return count
}

// Size returns the configured pool size.
func (p *Pool) Size() int {
	return p.size
}
