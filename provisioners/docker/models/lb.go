package models

import (
	"fmt"
	"strings"

	backendv1 "github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
)

type LB struct {
	// io.seagalia.flock.lb.policy, round_robin|weighted_round_robin|least_request|weighted_least_request|ring_hash|maglev,
	// round_robin when unset
	Policy *LBPolicy `mapstructure:"policy"`
}

type LBPolicy backendv1.Backend_LbPolicy

func (l *LBPolicy) UnmarshalMapstructure(input any) error {
	str, ok := input.(string)
	if !ok {
		return fmt.Errorf("expected string, got %T", input)
	}
	lbPol, ok := backendv1.Backend_LbPolicy_value[strings.ToUpper(str)]
	if !ok {
		return fmt.Errorf("invalid lb policy: %s", str)
	}
	*l = LBPolicy(lbPol)
	return nil
}
