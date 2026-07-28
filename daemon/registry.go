package daemon

import (
	"fmt"
	"sync"

	"github.com/thompsy/firecracker-sandbox/firevm"
)

type VM struct {
	State *firevm.State
}

type registry struct {
	vms map[int]*VM
	mu  sync.Mutex
}

func newRegistry() *registry {
	return &registry{
		vms: make(map[int]*VM),
	}
}

// Add inserts the given VM into the registry.
func (r *registry) Add(vm *VM) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.vms[vm.State.ID] = vm
}

func (r *registry) Get(id int) (*firevm.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	s, ok := r.vms[id]
	if !ok {
		return nil, fmt.Errorf("no VM found for id: %d", id)
	}
	return s.State, nil
}

func (r *registry) List() []*firevm.State {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]*firevm.State, 0, len(r.vms))
	for _, vm := range r.vms {
		out = append(out, vm.State)
	}

	return out
}

func (r *registry) Remove(id int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.vms, id)
}
