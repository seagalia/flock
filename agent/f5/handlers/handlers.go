// Package handlers receives the resources of the controller for the F5 agent. Nothing is applied here: the handlers
// tell the reconciler something changed, and the backend handler discovers the endpoints the pools are built from.
package handlers

import (
	"context"

	"github.com/seagalia/flock/sdk/agentsdk/adshandlers"
	"google.golang.org/protobuf/proto"
)

type changeHandler[T proto.Message] struct {
	name   string
	notify func()
}

// NewChangeHandler calls notify on every add and remove. The AdsHandler deletes a removed resource from the store
// after its handlers return: the debounce of the reconciler leaves it the time to.
func NewChangeHandler[T proto.Message](name string, notify func()) adshandlers.MessageHandler[T] {
	return &changeHandler[T]{name: name, notify: notify}
}

func (h *changeHandler[T]) OnMessageAdded(_ context.Context, _ T) error {
	h.notify()
	return nil
}

func (h *changeHandler[T]) OnMessageRemove(_ context.Context, _ T) error {
	h.notify()
	return nil
}

func (h *changeHandler[T]) Name() string {
	return h.name
}
