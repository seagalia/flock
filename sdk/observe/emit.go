package observe

import (
	"github.com/ArthurHlt/emitter"
	"github.com/seagalia/flock/sdk/flock/api/observer/v1"
)

type ListenerFuncSubject func(*emitter.EventOf[*observer.Subject])

func (fn ListenerFuncSubject) Observe(e emitter.Event) {
	typedEvent, ok := e.(*emitter.EventOf[*observer.Subject])
	if !ok {
		return
	}
	fn(typedEvent)
}

var e = emitter.New(100)

func On(eventType observer.EventType, listener emitter.Listener, middlewares ...func(emitter.Event)) {
	e.On(observer.EventType_name[int32(eventType)], listener, middlewares...)
}

func OnAll(listener emitter.Listener, middlewares ...func(emitter.Event)) {
	e.On("*", listener, middlewares...)
}

func Once(eventType observer.EventType, listener emitter.Listener, middlewares ...func(emitter.Event)) {
	e.Once(observer.EventType_name[int32(eventType)], listener, middlewares...)
}

func Off(eventType observer.EventType, listener ...emitter.Listener) {
	e.Off(observer.EventType_name[int32(eventType)], listener...)
}

// OffAll removes listeners from every topic, all of them when none is given
func OffAll(listener ...emitter.Listener) {
	e.Off("*", listener...)
}

func Listeners(eventType observer.EventType) []emitter.Listener {
	return e.Listeners(observer.EventType_name[int32(eventType)])
}

func Topics() []string {
	return e.Topics()
}

func Emit(eventType observer.EventType, data any) chan struct{} {
	if subject, ok := data.(*observer.Subject); ok {
		return EmitSubject(eventType, subject)
	}
	return e.Emit(emitter.NewEvent(observer.EventType_name[int32(eventType)], data))
}

func EmitSubject(eventType observer.EventType, subject *observer.Subject) chan struct{} {
	return e.Emit(emitter.NewEventOf[*observer.Subject](observer.EventType_name[int32(eventType)], subject))
}
