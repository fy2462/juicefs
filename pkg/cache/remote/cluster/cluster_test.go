package cluster

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPlacementNormalizesNodesAndCapsReplicas(t *testing.T) {
	p := NewPlacement([]string{" n2 ", "", "n1", "n3"}, 10)

	nodes := p.Candidates("chunks/0/0/1_0_4")

	require.Len(t, nodes, 3)
	require.ElementsMatch(t, []string{"n1", "n2", "n3"}, nodes)
}

func TestPlacementIsStableForSameKey(t *testing.T) {
	p := NewPlacement([]string{"n1", "n2", "n3"}, 2)

	first := p.Candidates("chunks/0/0/1_0_4")
	second := p.Candidates("chunks/0/0/1_0_4")

	require.Equal(t, first, second)
}

func TestPlacementDefaultsReplicaToOne(t *testing.T) {
	p := NewPlacement([]string{"n1", "n2"}, 0)

	nodes := p.Candidates("chunks/0/0/1_0_4")

	require.Len(t, nodes, 1)
}

func TestHealthAllowsHealthyCandidates(t *testing.T) {
	h := NewHealth(Options{FailThreshold: 1, Cooldown: time.Second})

	nodes := h.Available([]string{"n1", "n2"})

	require.Equal(t, []string{"n1", "n2"}, nodes)
}
