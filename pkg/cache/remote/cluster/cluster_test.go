package cluster

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type recordingObserver struct {
	events []string
}

func (o *recordingObserver) NodeFailure(node string) {
	o.events = append(o.events, "failure:"+node)
}

func (o *recordingObserver) NodeDown(node string) {
	o.events = append(o.events, "down:"+node)
}

func (o *recordingObserver) NodeRecovered(node string) {
	o.events = append(o.events, "recovered:"+node)
}

func (o *recordingObserver) NodeSkipped(node, op string) {
	o.events = append(o.events, fmt.Sprintf("skip:%s:%s", op, node))
}

func (o *recordingObserver) NodeProbe(node string, result ProbeResult) {
	o.events = append(o.events, fmt.Sprintf("probe:%s:%s", result, node))
}

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

func TestHealthSkipsNodeDuringCooldown(t *testing.T) {
	now := time.Unix(100, 0)
	h := NewHealth(Options{
		FailThreshold: 2,
		Cooldown:      time.Second,
		Now:           func() time.Time { return now },
	})

	h.MarkFailure("n1")
	require.Equal(t, []string{"n1", "n2"}, h.Available([]string{"n1", "n2"}))

	h.MarkFailure("n1")
	require.Equal(t, []string{"n2"}, h.Available([]string{"n1", "n2"}))
}

func TestHealthAllowsProbeAfterCooldown(t *testing.T) {
	now := time.Unix(100, 0)
	h := NewHealth(Options{
		FailThreshold: 1,
		Cooldown:      time.Second,
		Now:           func() time.Time { return now },
	})

	h.MarkFailure("n1")
	require.Empty(t, h.Available([]string{"n1"}))

	now = now.Add(time.Second)
	require.Equal(t, []string{"n1"}, h.Available([]string{"n1"}))
}

func TestHealthSuccessRecoversNode(t *testing.T) {
	now := time.Unix(100, 0)
	h := NewHealth(Options{
		FailThreshold: 1,
		Cooldown:      time.Second,
		Now:           func() time.Time { return now },
	})

	h.MarkFailure("n1")
	require.Empty(t, h.Available([]string{"n1"}))

	h.MarkSuccess("n1")
	require.Equal(t, []string{"n1"}, h.Available([]string{"n1"}))
}

func TestHealthObserverRecordsDownSkipProbeAndRecovery(t *testing.T) {
	now := time.Unix(100, 0)
	observer := &recordingObserver{}
	h := NewHealth(Options{
		FailThreshold: 1,
		Cooldown:      time.Second,
		Now:           func() time.Time { return now },
		Observer:      observer,
	})

	h.MarkFailure("n1")
	require.Empty(t, h.AvailableForOp([]string{"n1"}, "get"))
	h.MarkProbe("n1", false)
	h.MarkProbe("n1", true)

	require.Equal(t, []string{
		"failure:n1",
		"down:n1",
		"skip:get:n1",
		"probe:failure:n1",
		"failure:n1",
		"probe:success:n1",
		"recovered:n1",
	}, observer.events)
	require.Equal(t, []string{"n1"}, h.Available([]string{"n1"}))
}
