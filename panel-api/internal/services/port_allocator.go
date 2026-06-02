package services

import (
	"context"
	"fmt"
	"math/rand"
	"sync"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// PortAllocator assigns unique listen ports from a configurable range
// for non-PHP runtime services. Each allocated port is persisted in the
// runtime_services table via the repository; the allocator checks both
// the DB and a local reservation set to avoid collisions.
//
// Thread-safe: concurrent allocations are serialised by a mutex.
type PortAllocator struct {
	mu       sync.Mutex
	repo     repository.RuntimeServiceRepository
	minPort  uint32
	maxPort  uint32
	reserved map[uint32]bool // in-flight reservations not yet persisted
}

// NewPortAllocator creates a port allocator that assigns ports in
// [minPort, maxPort]. The range should be wide enough to support all
// expected concurrent runtime services.
func NewPortAllocator(repo repository.RuntimeServiceRepository, minPort, maxPort uint32) *PortAllocator {
	if minPort == 0 {
		minPort = 10000
	}
	if maxPort == 0 {
		maxPort = 60000
	}
	return &PortAllocator{
		repo:     repo,
		minPort:  minPort,
		maxPort:  maxPort,
		reserved: make(map[uint32]bool),
	}
}

// Allocate finds an unused port in the configured range. It checks the
// runtime_services table (via the repository) and the local reservation
// set. Returns an error if the entire range is exhausted.
//
// The caller must persist the returned port to the runtime_services row
// within a reasonable time; call Release if the creation fails.
func (a *PortAllocator) Allocate(ctx context.Context) (uint32, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	rangeSize := a.maxPort - a.minPort + 1

	// Try random ports first (avoids sequential scanning bias).
	for attempts := 0; attempts < 100; attempts++ {
		port := a.minPort + uint32(rand.Intn(int(rangeSize)))
		if a.reserved[port] {
			continue
		}
		inUse, err := a.repo.IsPortInUse(ctx, port)
		if err != nil {
			return 0, fmt.Errorf("port allocator: DB check failed: %w", err)
		}
		if !inUse {
			a.reserved[port] = true
			return port, nil
		}
	}

	// Fallback: linear scan through the entire range.
	for port := a.minPort; port <= a.maxPort; port++ {
		if a.reserved[port] {
			continue
		}
		inUse, err := a.repo.IsPortInUse(ctx, port)
		if err != nil {
			return 0, fmt.Errorf("port allocator: DB check failed: %w", err)
		}
		if !inUse {
			a.reserved[port] = true
			return port, nil
		}
	}

	return 0, fmt.Errorf("port allocator: all ports in range %d–%d are in use", a.minPort, a.maxPort)
}

// Release removes a port from the in-flight reservation set. Call this
// when a runtime service creation fails after Allocate returned a port
// but before the row was persisted.
func (a *PortAllocator) Release(port uint32) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.reserved, port)
}
