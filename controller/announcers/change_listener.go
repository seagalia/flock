package announcers

import (
	"sync"

	"github.com/ArthurHlt/emitter"
	"github.com/rs/zerolog"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcdiscovery "github.com/seagalia/flock/sdk/flock/services/discovery/v1"
	"github.com/seagalia/flock/sdk/logging"
)

// ChangeListener gives a listener the changes of the resources matching its filters. The announcer
// sends every change of the instance: a resource that stops matching is removed from the listener,
// a resource the listener never received is not removed from it.
//
// Events reach it in any order: the emitter delivers each one from its own goroutine and the announcer
// emits from several. So an event only says which resources moved, what is sent is their state now in
// the announcer: the last event of a resource always ends the listener on its latest state.
type ChangeListener struct {
	aListen Listener
	filters svccore.ListFilters
	// current gives the resource the announcer holds now, false when it holds none
	current func(uuid string) (*svcdiscovery.Resource, bool)
	entry   zerolog.Logger
	// uuids of the resources the listener has
	sent      map[string]bool
	sentMutex sync.Mutex
}

func NewChangeListener(
	aListen Listener, filters svccore.ListFilters, sent map[string]bool,
	current func(uuid string) (*svcdiscovery.Resource, bool),
) *ChangeListener {
	return &ChangeListener{
		aListen: aListen,
		filters: filters,
		current: current,
		sent:    sent,
		entry:   logging.Component("change_listener"),
	}
}

func (l *ChangeListener) Observe(e emitter.Event) {
	typedEvent, ok := e.(*emitter.EventOf[[]*svcdiscovery.Resource])
	if !ok {
		return
	}
	if zerolog.GlobalLevel() <= zerolog.DebugLevel {
		for _, res := range typedEvent.TypedSubject() {
			l.entry.Debug().
				Str("topic", typedEvent.Topic()).
				Str("res_uuid", res.Uuid).
				Str("res_type", res.Resource.GetTypeUrl()).
				Msg("Receiving")
		}
	}

	changes, removes := l.resolve(typedEvent.TypedSubject())
	if len(changes) > 0 {
		l.aListen.OnAnnounce(ChangeEvent, changes)
	}
	if len(removes) > 0 {
		l.aListen.OnAnnounce(RemoveEvent, removes)
	}
}

func (l *ChangeListener) resolve(resources []*svcdiscovery.Resource) (changes, removes []*svcdiscovery.Resource) {
	l.sentMutex.Lock()
	defer l.sentMutex.Unlock()
	for _, res := range resources {
		current, held := l.current(res.GetUuid())
		if held && matchFilters(l.filters, current) {
			l.sent[res.GetUuid()] = true
			changes = append(changes, current)
			continue
		}
		// without filters the listener may have it from its first snapshot, never marked as sent
		mayHaveIt := l.sent[res.GetUuid()] || len(l.filters) == 0
		if mayHaveIt {
			delete(l.sent, res.GetUuid())
			removes = append(removes, res)
		}
	}
	return changes, removes
}

func matchFilters(filters svccore.ListFilters, res *svcdiscovery.Resource) bool {
	if len(filters) == 0 {
		return true
	}
	message, err := res.GetResource().UnmarshalNew()
	if err != nil {
		return false
	}
	return filters.MatchAnd(message)
}
