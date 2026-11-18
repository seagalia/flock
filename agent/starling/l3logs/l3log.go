package l3logs

import "context"

const DefaultNflogGroup = 15182

type L3Logger interface {
	Run(ctx context.Context, afterInit func()) error
}
