package cluster

import "time"

type Options struct {
	FailThreshold int
	Cooldown      time.Duration
	Now           func() time.Time
}

type Health struct {
	options Options
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
	return &Health{options: options}
}

func (h *Health) Available(nodes []string) []string {
	return append([]string(nil), nodes...)
}
