package cluster

import (
	"sync"
	"time"
)

type Options struct {
	FailThreshold int
	Cooldown      time.Duration
	Now           func() time.Time
}

type Health struct {
	mu      sync.Mutex
	options Options
	nodes   map[string]*nodeState
}

type nodeState struct {
	failures int
	downAt   time.Time
}

func NewHealth(options Options) *Health {
	if options.FailThreshold <= 0 {
		options.FailThreshold = 1
	}
	if options.Cooldown <= 0 {
		options.Cooldown = 5 * time.Second
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Health{options: options, nodes: make(map[string]*nodeState)}
}

func (h *Health) Available(nodes []string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.options.Now()
	available := make([]string, 0, len(nodes))
	for _, node := range nodes {
		state := h.nodes[node]
		if state == nil || state.failures < h.options.FailThreshold || !now.Before(state.downAt.Add(h.options.Cooldown)) {
			available = append(available, node)
		}
	}
	return available
}

func (h *Health) MarkFailure(node string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.nodes[node]
	if state == nil {
		state = &nodeState{}
		h.nodes[node] = state
	}
	state.failures++
	if state.failures >= h.options.FailThreshold {
		state.downAt = h.options.Now()
	}
}

func (h *Health) MarkSuccess(node string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.nodes, node)
}
