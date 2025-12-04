package pool

import "sync"

// Resettable is an interface for objects that can be reset to their initial state
type Resettable interface {
	Reset()
}

// Pool is a generic object pool based on sync.Pool
type Pool[T any] struct {
	pool sync.Pool
}

// New creates a new generic pool with a factory function
func New[T any](factory func() T) *Pool[T] {
	return &Pool[T]{
		pool: sync.Pool{
			New: func() interface{} {
				return factory()
			},
		},
	}
}

// Get retrieves an object from the pool
func (p *Pool[T]) Get() T {
	return p.pool.Get().(T)
}

// Put returns an object to the pool
// If the object implements Resettable, it will be reset before being returned
func (p *Pool[T]) Put(obj T) {
	// If the object implements Resettable, reset it
	if r, ok := any(obj).(Resettable); ok {
		r.Reset()
	}
	p.pool.Put(obj)
}
