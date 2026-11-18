package stores

import (
	"slices"
	"strings"
	"sync"

	"github.com/seagalia/flock/sdk/flock/services/agent/status/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
)

// stateResource is what an agent knows of a resource: in progress (ADDING, REMOVING), ERRORED with the error, or OK
// once applied. The version is the one the controller sent with the resource, it outlives the changes of state.
type stateResource struct {
	message proto.Message
	err     error
	state   agentstatus.StatusResponse_State
	version string
}

// sourceNotes are the notes one writer gives on a resource, so writers on the same resource never erase each other
type sourceNotes struct {
	source string
	notes  []*flocktype.Note
}

type StateStore struct {
	mutex  sync.RWMutex
	states map[stateKey]stateResource
	// notes are kept apart from the states: an agent clears the state of an applied resource but its notes stay true
	// as long as the resource keeps the same content
	notesMutex sync.Mutex
	notes      map[stateKey][]sourceNotes
}

func NewStateStore() *StateStore {
	return &StateStore{
		states: map[stateKey]stateResource{},
		notes:  map[stateKey][]sourceNotes{},
	}
}

type stateKey struct {
	typeUrl string
	uuid    string
}

func (s *StateStore) makeKey(typeUrl, uuid string) stateKey {
	return stateKey{typeUrl: typeUrl, uuid: uuid}
}

func (s *StateStore) makeKeyFromMessage(message proto.Message) stateKey {
	typeUrl := helpers.TypeUrlFromMessage(message)
	uuid := helpers.GetIdentifier(message)
	return s.makeKey(typeUrl, uuid)
}

func (s *StateStore) store(key stateKey, message proto.Message, state agentstatus.StatusResponse_State, err error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.states[key] = stateResource{
		message: message,
		state:   state,
		err:     err,
		version: s.states[key].version,
	}
}

func (s *StateStore) AddError(message proto.Message, err error) {
	s.store(s.makeKeyFromMessage(message), message, agentstatus.StatusResponse_ERRORED, err)
}

func (s *StateStore) SetState(message proto.Message, state agentstatus.StatusResponse_State) {
	s.store(s.makeKeyFromMessage(message), message, state, nil)
}

func (s *StateStore) SetStateTyped(typeUrl, uuid string, state agentstatus.StatusResponse_State) {
	s.store(s.makeKey(typeUrl, uuid), nil, state, nil)
}

// ClearState marks a resource applied, it drops the error but keeps the version.
func (s *StateStore) ClearState(typeUrl, uuid string) {
	s.store(s.makeKey(typeUrl, uuid), nil, agentstatus.StatusResponse_OK, nil)
}

func (s *StateStore) SetVersion(typeUrl, uuid, version string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	key := s.makeKey(typeUrl, uuid)
	stateRes := s.states[key]
	stateRes.version = version
	s.states[key] = stateRes
}

// Version gives the version the controller sent with the resource, empty when it never sent it.
func (s *StateStore) Version(typeUrl, uuid string) string {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.states[s.makeKey(typeUrl, uuid)].version
}

func (s *StateStore) Get(typeUrl, uuid string) (proto.Message, agentstatus.StatusResponse_State, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	stateRes, ok := s.states[s.makeKey(typeUrl, uuid)]
	if !ok {
		return nil, agentstatus.StatusResponse_UNKNOWN, nil
	}
	return stateRes.message, stateRes.state, stateRes.err
}

func (s *StateStore) Range(typeUrl string, f func(message proto.Message, state agentstatus.StatusResponse_State, err error)) {
	s.mutex.RLock()
	ofType := make([]stateResource, 0)
	for key, stateRes := range s.states {
		if key.typeUrl == typeUrl {
			ofType = append(ofType, stateRes)
		}
	}
	s.mutex.RUnlock()
	// f may change the store, it runs once the lock is released
	for _, stateRes := range ofType {
		f(stateRes.message, stateRes.state, stateRes.err)
	}
}

// DeleteTyped forgets a resource the agent no longer holds, its version included.
func (s *StateStore) DeleteTyped(typeUrl, uuid string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	delete(s.states, s.makeKey(typeUrl, uuid))
}

// SetNotes replaces the notes a source gives on a resource, an empty list removes them.
func (s *StateStore) SetNotes(typeUrl, uuid, source string, notes []*flocktype.Note) {
	key := s.makeKey(typeUrl, uuid)
	s.notesMutex.Lock()
	defer s.notesMutex.Unlock()
	bySource := slices.DeleteFunc(s.notes[key], func(n sourceNotes) bool { return n.source == source })
	if len(notes) > 0 {
		at, _ := slices.BinarySearchFunc(bySource, source, func(n sourceNotes, source string) int {
			return strings.Compare(n.source, source)
		})
		bySource = slices.Insert(bySource, at, sourceNotes{source: source, notes: notes})
	}
	if len(bySource) == 0 {
		delete(s.notes, key)
		return
	}
	s.notes[key] = bySource
}

// Notes gives the notes of every source on a resource, sorted by source.
func (s *StateStore) Notes(typeUrl, uuid string) []*flocktype.Note {
	s.notesMutex.Lock()
	defer s.notesMutex.Unlock()
	var notes []*flocktype.Note
	for _, fromSource := range s.notes[s.makeKey(typeUrl, uuid)] {
		notes = append(notes, fromSource.notes...)
	}
	return notes
}
