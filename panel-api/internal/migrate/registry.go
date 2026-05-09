package migrate

import (
	"fmt"
	"sync"
)

// Registry maps source-kind strings (models.MigrationSource* constants)
// to their Discoverer factory. Each importer package registers itself
// from an init() call; serve.go imports the package for side effects.
//
// Lookup is the only path admin REST + the Step-8 UI use to pick the
// right importer for a given migration_jobs.source_kind row.
type Registry struct {
	mu      sync.RWMutex
	factory map[string]func() Discoverer
}

var defaultRegistry = &Registry{factory: map[string]func() Discoverer{}}

// Register adds a Discoverer factory for one source kind. Panics on
// duplicate registration — importer init() collisions surface at
// process start, not silently at lookup time.
func Register(kind string, fn func() Discoverer) {
	defaultRegistry.mu.Lock()
	defer defaultRegistry.mu.Unlock()
	if _, exists := defaultRegistry.factory[kind]; exists {
		panic(fmt.Sprintf("migrate.Register: duplicate kind %q", kind))
	}
	defaultRegistry.factory[kind] = fn
}

// Get returns a fresh Discoverer for the requested kind. Returns
// (nil, error) when the kind is unknown — caller surfaces 400.
func Get(kind string) (Discoverer, error) {
	defaultRegistry.mu.RLock()
	defer defaultRegistry.mu.RUnlock()
	fn, ok := defaultRegistry.factory[kind]
	if !ok {
		return nil, fmt.Errorf("migrate: unknown source kind %q (registered: %v)", kind, defaultRegistry.kindsLocked())
	}
	return fn(), nil
}

// Kinds returns every registered kind for the admin UI's source-
// picker dropdown. Order is non-deterministic; caller sorts.
func Kinds() []string {
	defaultRegistry.mu.RLock()
	defer defaultRegistry.mu.RUnlock()
	return defaultRegistry.kindsLocked()
}

func (r *Registry) kindsLocked() []string {
	out := make([]string, 0, len(r.factory))
	for k := range r.factory {
		out = append(out, k)
	}
	return out
}
