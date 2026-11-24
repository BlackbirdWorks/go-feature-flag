package postgresqlretriever

import (
	"context"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

type poolEntry struct {
	pool     *pgxpool.Pool
	refCount int
}

// PoolRegistry shares the connection pools between the retrievers that use the same database URI:
// a pool is created for the first retriever that asks for a URI, reused by the next ones, and closed
// when the last one releases it. The retrievers of the flag sets of a relay proxy get the registry of
// the relay (Retriever.Pools), so that flag sets on one database do not each open their own pool; a
// retriever without a registry gets one of its own, and shares nothing.
type PoolRegistry struct {
	pools map[string]*poolEntry
	mu    sync.Mutex
}

// NewPoolRegistry creates an empty registry.
func NewPoolRegistry() *PoolRegistry {
	return &PoolRegistry{pools: make(map[string]*poolEntry)}
}

// GetPool returns a pool for a given URI, creating it if needed.
func (p *PoolRegistry) GetPool(ctx context.Context, uri string) (*pgxpool.Pool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// If already exists, bump refCount
	if entry, ok := p.pools[uri]; ok {
		entry.refCount++

		return entry.pool, nil
	}

	// Create a new pool
	pool, err := pgxpool.New(ctx, uri)
	if err != nil {
		return nil, err
	}
	if pingErr := pool.Ping(ctx); pingErr != nil {
		pool.Close()

		return nil, pingErr
	}

	p.pools[uri] = &poolEntry{
		pool:     pool,
		refCount: 1,
	}

	return pool, nil
}

// ReleasePool decreases refCount and closes/removes when it hits zero.
func (p *PoolRegistry) ReleasePool(_ context.Context, uri string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	entry, ok := p.pools[uri]
	if !ok {
		return // nothing to do
	}

	entry.refCount--
	if entry.refCount <= 0 {
		entry.pool.Close()
		delete(p.pools, uri)
	}
}
