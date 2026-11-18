package adshandlers

import (
	"context"

	"google.golang.org/protobuf/proto"
)

//go:generate go tool mockgen -destination=../testhelpers/mock_handler_interfaces.go -package=testhelpers --source=interfaces.go

type Cleanable interface {
	Clean() error
}

type Stoppable interface {
	Stop() error
}

type MessageHandler[T proto.Message] interface {
	OnMessageAdded(ctx context.Context, message T) error
	OnMessageRemove(ctx context.Context, message T) error
	Name() string
}
