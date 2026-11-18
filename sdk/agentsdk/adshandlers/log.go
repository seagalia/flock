package adshandlers

import (
	"context"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/logging"
	"google.golang.org/protobuf/proto"
)

// LogHandler is a MessageHandler logging the messages it receives, for development without a full stack
type LogHandler[T proto.Message] struct {
	entry zerolog.Logger
}

func NewLogHandler[T proto.Message]() *LogHandler[T] {
	return &LogHandler[T]{
		entry: logging.Component("log_handler").With().Str("for_type", helpers.TypeUrl[T]()).Logger(),
	}
}

func (l LogHandler[T]) OnMessageAdded(ctx context.Context, message T) error {
	l.entry.Info().Interface("message", message).Msg("OnMessageAdded")
	return nil
}

func (l LogHandler[T]) OnMessageRemove(ctx context.Context, message T) error {
	l.entry.Info().Interface("message", message).Msg("OnMessageRemove")
	return nil
}

func (l LogHandler[T]) Name() string {
	return "LogHandler for " + helpers.TypeUrl[T]()
}
