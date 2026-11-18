package lockers

import (
	"context"
	"sync"
)

// Subjects of the locks of the controller.
const (
	// InstanceVips a vip is on one instance of a zone at most: the check and the change of the vips of an instance
	// must not interleave between instances
	InstanceVips = "instance-vips"
	// ZonePools a pool change reads the zone and stores it changed: two changes of the pools must not interleave
	ZonePools = "zone-pools"
)

// Locker serializes the changes of a subject. Lock waits until the subject is free or ctx is done,
// Unlock frees a subject locked by this controller, even when ctx is done. Subjects locked together are always locked
// in the same order (ZonePools before InstanceVips) so two holders never wait for each other.
type Locker interface {
	Lock(ctx context.Context, subject string) error
	Unlock(ctx context.Context, subject string)
}

// Memory locks inside this controller only: Db takes it first, so callers of one controller wait for
// each other here instead of polling the database.
type Memory struct {
	mutex sync.Mutex
	// a channel of one slot per subject: unlike a mutex, waiting for it can stop on ctx
	subjects map[string]chan struct{}
}

func NewMemory() *Memory {
	return &Memory{
		subjects: make(map[string]chan struct{}),
	}
}

func (m *Memory) Lock(ctx context.Context, subject string) error {
	select {
	case m.subjectSlot(subject) <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Memory) Unlock(_ context.Context, subject string) {
	<-m.subjectSlot(subject)
}

func (m *Memory) subjectSlot(subject string) chan struct{} {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	slot, ok := m.subjects[subject]
	if !ok {
		slot = make(chan struct{}, 1)
		m.subjects[subject] = slot
	}
	return slot
}
