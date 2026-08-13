package layout

import "sync"

// Registry maps layout GUIDs to their parsed Layout descriptors.
// All methods are safe for concurrent use.
type Registry struct {
	mu      sync.RWMutex
	layouts map[GUID]*Layout
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{layouts: make(map[GUID]*Layout)}
}

// Register adds or replaces the Layout for its GUID.
// Callers should call PendingQueue.Drain immediately after to reprocess any
// data messages that arrived before this layout was known.
func (r *Registry) Register(l *Layout) {
	r.mu.Lock()
	r.layouts[l.GUID] = l
	r.mu.Unlock()
}

// Get looks up a Layout by GUID.
// Returns (nil, false) when no layout with that GUID has been registered.
func (r *Registry) Get(guid GUID) (*Layout, bool) {
	r.mu.RLock()
	l, ok := r.layouts[guid]
	r.mu.RUnlock()
	return l, ok
}

// Len returns the number of currently registered layouts.
func (r *Registry) Len() int {
	r.mu.RLock()
	n := len(r.layouts)
	r.mu.RUnlock()
	return n
}
