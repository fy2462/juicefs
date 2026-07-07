package cluster

import (
	"sync"
	"time"
)

type Options struct {
	FailThreshold int
	Cooldown      time.Duration
	Now           func() time.Time
	Observer      Observer
}

type ProbeResult string

const (
	ProbeSuccess ProbeResult = "success"
	ProbeFailure ProbeResult = "failure"
)

type Observer interface {
	NodeFailure(node string)
	NodeDown(node string)
	NodeRecovered(node string)
	NodeSkipped(node, op string)
	NodeProbe(node string, result ProbeResult)
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
	return h.AvailableForOp(nodes, "")
}

func (h *Health) AvailableForOp(nodes []string, op string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.options.Now()
	available := make([]string, 0, len(nodes))
	for _, node := range nodes {
		state := h.nodes[node]
		if state == nil || state.failures < h.options.FailThreshold || !now.Before(state.downAt.Add(h.options.Cooldown)) {
			available = append(available, node)
			continue
		}
		if h.options.Observer != nil {
			h.options.Observer.NodeSkipped(node, op)
		}
	}
	return available
}

func (h *Health) Unhealthy(nodes []string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	unhealthy := make([]string, 0, len(nodes))
	for _, node := range nodes {
		state := h.nodes[node]
		if state != nil && state.failures >= h.options.FailThreshold {
			unhealthy = append(unhealthy, node)
		}
	}
	return unhealthy
}

func (h *Health) MarkFailure(node string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.nodes[node]
	if state == nil {
		state = &nodeState{}
		h.nodes[node] = state
	}
	wasDown := state.failures >= h.options.FailThreshold
	state.failures++
	if h.options.Observer != nil {
		h.options.Observer.NodeFailure(node)
	}
	if state.failures >= h.options.FailThreshold {
		state.downAt = h.options.Now()
		if !wasDown && h.options.Observer != nil {
			h.options.Observer.NodeDown(node)
		}
	}
}

func (h *Health) MarkSuccess(node string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.nodes[node]
	if state != nil && state.failures >= h.options.FailThreshold && h.options.Observer != nil {
		h.options.Observer.NodeRecovered(node)
	}
	delete(h.nodes, node)
}

func (h *Health) MarkProbe(node string, ok bool) {
	if ok {
		if h.options.Observer != nil {
			h.options.Observer.NodeProbe(node, ProbeSuccess)
		}
		h.MarkSuccess(node)
		return
	}
	if h.options.Observer != nil {
		h.options.Observer.NodeProbe(node, ProbeFailure)
	}
	h.MarkFailure(node)
}
