package storage

import (
	"fmt"
	"sync"
)

// Registry holds named StorageDriver implementations.
type Registry struct {
	mu      sync.RWMutex
	drivers map[string]StorageDriver
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{drivers: make(map[string]StorageDriver)}
}

// Register adds d under d.Name(). Panics if the name is already registered.
func (r *Registry) Register(d StorageDriver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.drivers[d.Name()]; ok {
		panic(fmt.Sprintf("storage: driver %q already registered", d.Name()))
	}
	r.drivers[d.Name()] = d
}

// Get returns the driver registered under name.
func (r *Registry) Get(name string) (StorageDriver, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.drivers[name]
	if !ok {
		return nil, fmt.Errorf("storage: driver %q not registered", name)
	}
	return d, nil
}

// Names returns the sorted list of registered driver names.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.drivers))
	for n := range r.drivers {
		out = append(out, n)
	}
	return out
}
