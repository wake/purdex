package core

import "fmt"

// ServiceRegistry is a simple type-erased service locator.
type ServiceRegistry struct {
	services map[string]any
}

// NewServiceRegistry creates an empty registry.
func NewServiceRegistry() *ServiceRegistry {
	return &ServiceRegistry{services: make(map[string]any)}
}

// Register stores a service under the given name.
func (r *ServiceRegistry) Register(name string, svc any) {
	r.services[name] = svc
}

// Get retrieves a service by name. Returns (nil, false) if not found.
func (r *ServiceRegistry) Get(name string) (any, bool) {
	svc, ok := r.services[name]
	return svc, ok
}

// MustGet retrieves a service by name, panicking if not found.
func (r *ServiceRegistry) MustGet(name string) any {
	svc, ok := r.services[name]
	if !ok {
		panic(fmt.Sprintf("service %q not registered", name))
	}
	return svc
}
