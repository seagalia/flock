package models

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
)

type HealthCheck struct {
	// io.seagalia.flock.health.path, path checked over http, only the docker healthcheck applies when unset
	Path string `mapstructure:"path"`
	// io.seagalia.flock.health.interval, e.g. 10s, 5s when unset
	Interval *Duration `mapstructure:"interval"`
	// io.seagalia.flock.health.timeout, e.g. 1s, 2s when unset
	Timeout *Duration `mapstructure:"timeout"`
	// io.seagalia.flock.health.healthy_threshold, 2 when unset
	HealthyThreshold *Threshold `mapstructure:"healthy_threshold"`
	// io.seagalia.flock.health.unhealthy_threshold, 3 when unset
	UnhealthyThreshold *Threshold `mapstructure:"unhealthy_threshold"`
	// io.seagalia.flock.health.status, expected codes as 200 or 200-399, 200-399 when unset
	Status *StatusRange `mapstructure:"status"`
}

type Duration time.Duration

func (d *Duration) UnmarshalMapstructure(input any) error {
	str, ok := input.(string)
	if !ok {
		return fmt.Errorf("expected string, got %T", input)
	}
	duration, err := time.ParseDuration(str)
	if err != nil {
		return fmt.Errorf("parse duration: %w", err)
	}
	*d = Duration(duration)
	return nil
}

// StatusRange keeps the end exclusive, like the Int64Range of the health check it becomes: 200-399 is [200, 400).
type StatusRange struct {
	Start int64
	End   int64
}

func (r *StatusRange) UnmarshalMapstructure(input any) error {
	str, ok := input.(string)
	if !ok {
		return fmt.Errorf("expected string, got %T", input)
	}
	first, last, isRange := strings.Cut(str, "-")
	if !isRange {
		last = first
	}
	start, err := strconv.ParseInt(strings.TrimSpace(first), 10, 64)
	if err != nil {
		return fmt.Errorf("parse status range start: %w", err)
	}
	end, err := strconv.ParseInt(strings.TrimSpace(last), 10, 64)
	if err != nil {
		return fmt.Errorf("parse status range end: %w", err)
	}
	if end < start {
		return fmt.Errorf("invalid status range %s: end before start", str)
	}
	r.Start = start
	r.End = end + 1
	return nil
}

func (r *StatusRange) toInt64Range() *flocktype.Int64Range {
	return &flocktype.Int64Range{Start: r.Start, End: r.End}
}
