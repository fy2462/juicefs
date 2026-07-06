package cluster

import (
	"hash/fnv"
	"sort"
	"strings"
)

type Placement struct {
	nodes    []string
	replicas int
}

func NewPlacement(nodes []string, replicas int) *Placement {
	p := &Placement{}
	for _, node := range nodes {
		node = strings.TrimSpace(node)
		if node != "" {
			p.nodes = append(p.nodes, node)
		}
	}
	p.replicas = replicas
	if p.replicas <= 0 {
		p.replicas = 1
	}
	if p.replicas > len(p.nodes) {
		p.replicas = len(p.nodes)
	}
	return p
}

func (p *Placement) Candidates(key string) []string {
	nodes := append([]string(nil), p.nodes...)
	sort.Slice(nodes, func(i, j int) bool {
		return score(key, nodes[i]) > score(key, nodes[j])
	})
	return nodes[:p.replicas]
}

func score(key, node string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(node))
	return h.Sum64()
}
