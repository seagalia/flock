//go:build !linux

package l3logs

import (
	"context"

	"github.com/seagalia/flock/sdk/agentsdk/stores"
)

type L3LoggerOthers struct {
}

func (l L3LoggerOthers) Run(ctx context.Context, afterInit func()) error {
	if afterInit != nil {
		afterInit()
	}
	return nil
}
func NewL3Logger(*stores.IpPortTranslator) L3Logger {
	return &L3LoggerOthers{}
}
