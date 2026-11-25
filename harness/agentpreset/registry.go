package agentpreset

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	ErrNotFound    = errors.New("Agent Preset not found")
	ErrUnavailable = errors.New("Agent Preset unavailable")
	ErrLocked      = errors.New("Agent Preset is locked after the first turn starts")
)

type Info struct {
	ID, Name, Description string
	Order                 int
	IsDefault             bool
	Broken                string
}

// Binding retains an immutable definition, not tool instances or captured
// context. Replacing a registry entry cannot mutate an existing binding.
type Binding struct{ definition *Definition }

type bindingKey struct{}

// WithBinding passes Go assembly policy to existing contribution adapters.
// It does not expose Agent management to the Node plugin process.
func WithBinding(ctx context.Context, binding *Binding) context.Context {
	return context.WithValue(ctx, bindingKey{}, binding)
}
func FromContext(ctx context.Context) *Binding {
	binding, _ := ctx.Value(bindingKey{}).(*Binding)
	return binding
}

func (b *Binding) ID() string {
	if b == nil || b.definition == nil {
		return ""
	}
	return b.definition.ID
}
func (b *Binding) Definition() Definition {
	if b == nil || b.definition == nil {
		return Definition{}
	}
	return clone(*b.definition)
}
func (b *Binding) Same(other *Binding) bool {
	return b != nil && other != nil && b.definition == other.definition
}

// Check performs current Host availability checks without creating an Agent.
type Check func(context.Context, Definition) error
type Registry struct {
	mu          sync.RWMutex
	defaultID   string
	definitions map[string]*Definition
	check       Check
}

func New(defaultID string, check Check) *Registry {
	return &Registry{defaultID: defaultID, definitions: map[string]*Definition{}, check: check}
}

func (r *Registry) Register(d Definition) (func(), error) {
	if err := Validate(d); err != nil {
		return nil, err
	}
	d = clone(d)
	r.mu.Lock()
	if _, exists := r.definitions[d.ID]; exists {
		r.mu.Unlock()
		return nil, fmt.Errorf("duplicate Agent Preset %q", d.ID)
	}
	r.definitions[d.ID] = &d
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		// A disposer for an older declaration must not remove its replacement.
		if r.definitions[d.ID] == &d {
			delete(r.definitions, d.ID)
		}
	}, nil
}

func (r *Registry) Replace(d Definition) error {
	if err := Validate(d); err != nil {
		return err
	}
	d = clone(d)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.definitions[d.ID]; !exists {
		return fmt.Errorf("%w: %s", ErrNotFound, d.ID)
	}
	r.definitions[d.ID] = &d
	return nil
}

func (r *Registry) SetDefault(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.definitions[id]; !exists {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	r.defaultID = id
	return nil
}

func (r *Registry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.definitions[id]; !exists {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	delete(r.definitions, id)
	return nil
}

func (r *Registry) Resolve(ctx context.Context, id string) (*Binding, error) {
	r.mu.RLock()
	if id == "" {
		id = r.defaultID
	}
	d := r.definitions[id]
	r.mu.RUnlock()
	if d == nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if len(d.RequiredSources) > 0 && r.check == nil {
		return nil, fmt.Errorf("%w: %s: required source catalog is unavailable", ErrUnavailable, id)
	}
	if r.check != nil {
		if err := r.check(ctx, clone(*d)); err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrUnavailable, id, err)
		}
	}
	return &Binding{definition: d}, nil
}

func (r *Registry) List(ctx context.Context) []Info {
	r.mu.RLock()
	defs := make([]*Definition, 0, len(r.definitions))
	for _, d := range r.definitions {
		defs = append(defs, d)
	}
	defaultID := r.defaultID
	r.mu.RUnlock()
	rows := make([]Info, 0, len(defs))
	for _, d := range defs {
		row := Info{ID: d.ID, Name: d.Name, Description: d.Description, Order: d.Order, IsDefault: d.ID == defaultID}
		if len(d.RequiredSources) > 0 && r.check == nil {
			row.Broken = "required source catalog is unavailable"
		}
		if r.check != nil {
			if err := r.check(ctx, clone(*d)); err != nil {
				row.Broken = err.Error()
			}
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Order != rows[j].Order {
			return rows[i].Order < rows[j].Order
		}
		return rows[i].ID < rows[j].ID
	})
	return rows
}
