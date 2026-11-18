package announcers

import (
	"context"

	"fmt"
	"strings"
	"sync"

	"github.com/ArthurHlt/emitter"
	"github.com/rs/zerolog"
	"github.com/samber/lo"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/api/observer/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcdiscovery "github.com/seagalia/flock/sdk/flock/services/discovery/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/logging"
	"github.com/seagalia/flock/sdk/storers"
	"google.golang.org/protobuf/proto"
)

const (
	ChangeEvent EventName = "change"
	RemoveEvent EventName = "remove"
)

//go:generate go tool mockgen -destination=mocks/mock_announcer.go -package=mocks . Listener

type emitSessStore map[string][]*svcdiscovery.Resource

func (es emitSessStore) addResource(evName EventName, typeUrl string, res *svcdiscovery.Resource) {
	topicName := makeAnnounceTopic(evName, typeUrl)
	if _, ok := es[topicName]; !ok {
		es[topicName] = []*svcdiscovery.Resource{}
	}
	es[topicName] = append(es[topicName], res)
}

type EventName string

type Listener interface {
	OnAnnounce(evName EventName, res []*svcdiscovery.Resource)
}

type Announcer struct {
	// ctx lives from the first agent connection to Close: bus events reach the announcer with no ctx, the store
	// reads they trigger run on it
	ctx          context.Context
	cancel       context.CancelFunc
	entry        zerolog.Logger
	instanceUuid string
	emit         *emitter.Emitter
	sm           storers.StoreManager
	instance     *instance.Instance
	rwMutex      *sync.RWMutex
	refMap       *sync.Map
	resMap       *sync.Map
	// resLocks serializes the changes of one subject, guarded by resLocksMutex
	resLocksMutex *sync.Mutex
	resLocks      map[subjectKey]*resLock
	listenersMap  *sync.Map
}

func NewAnnouncer(ctx context.Context, instanceUuid string, sm storers.StoreManager) *Announcer {
	ctx, cancel := context.WithCancel(ctx)
	a := &Announcer{
		ctx:           ctx,
		cancel:        cancel,
		entry:         logging.Component("announcer").With().Str("instance_uuid", instanceUuid).Logger(),
		instanceUuid:  instanceUuid,
		emit:          emitter.New(30),
		sm:            sm,
		rwMutex:       &sync.RWMutex{},
		refMap:        &sync.Map{},
		resMap:        &sync.Map{},
		resLocksMutex: &sync.Mutex{},
		resLocks:      make(map[subjectKey]*resLock),
		listenersMap:  &sync.Map{},
		instance:      &instance.Instance{},
	}

	return a
}

func (a *Announcer) Init() {
	ctx := a.ctx
	session := make(emitSessStore)
	a.updateInstance(ctx, session, observer.EventType_UPDATE_SUBJECT, &observer.Subject{
		TypeUrl:    helpers.TypeUrl[*instance.Instance](),
		Identifier: a.instanceUuid,
	})
	a.emitSession(session)
}

type resLock struct {
	mutex   sync.Mutex
	holders int
}

// lockRes gives the unlock of the very mutex it locked. The lock of a subject is forgotten once nobody holds nor
// waits for it: removing it while another goroutine waits would let that one unlock a newer, unlocked mutex.
func (a *Announcer) lockRes(subject *observer.Subject) (unlock func()) {
	key := a.makeKey(subject)
	a.resLocksMutex.Lock()
	lock, ok := a.resLocks[key]
	if !ok {
		lock = &resLock{}
		a.resLocks[key] = lock
	}
	lock.holders++
	a.resLocksMutex.Unlock()

	lock.mutex.Lock()
	return func() {
		lock.mutex.Unlock()
		a.resLocksMutex.Lock()
		defer a.resLocksMutex.Unlock()
		lock.holders--
		if lock.holders == 0 {
			delete(a.resLocks, key)
		}
	}
}

func (a *Announcer) Observe(e emitter.Event) {
	ctx := a.ctx
	a.rwMutex.RLock()
	defer a.rwMutex.RUnlock()
	session := make(emitSessStore)
	subject, ok := e.(*emitter.EventOf[*observer.Subject])
	if !ok {
		return
	}
	evType := observer.EventType(observer.EventType_value[subject.Topic()])
	a.entry.Debug().
		Str("event_type", evType.String()).
		Str("subject_type", subject.TypedSubject().TypeUrl).
		Msg("Receive event")
	// if an event from main event bus is received, we force to clean the ref map to ensure delete this subject
	if evType == observer.EventType_DELETE_SUBJECT {
		a.delAllRef(subject.TypedSubject())
	}
	if subject.TypedSubject().TypeUrl == helpers.TypeUrl[*instance.Instance]() {
		a.updateInstance(ctx, session, evType, subject.TypedSubject())
	}
	if subject.TypedSubject().TypeUrl == helpers.TypeUrl[*backend.Backend]() {
		a.updateBackend(ctx, session, evType, subject.TypedSubject())
	}
	if subject.TypedSubject().TypeUrl == helpers.TypeUrl[*vip.VIP]() {
		a.updateVip(ctx, session, evType, subject.TypedSubject())
	}
	if subject.TypedSubject().TypeUrl == helpers.TypeUrl[*vs.VirtualServer]() {
		a.updateVirtualServer(ctx, session, evType, subject.TypedSubject())
	}
	if subject.TypedSubject().TypeUrl == helpers.TypeUrl[*cert.Certificate]() {
		a.updateCertificate(ctx, session, evType, subject.TypedSubject())
	}
	if subject.TypedSubject().TypeUrl == helpers.TypeUrl[*secret.Secret]() {
		a.updateSecret(ctx, session, evType, subject.TypedSubject())
	}
	a.emitSession(session)
}

type subjectKey struct {
	typeUrl    string
	identifier string
}

func (a *Announcer) makeKey(subject *observer.Subject) subjectKey {
	return subjectKey{typeUrl: subject.GetTypeUrl(), identifier: subject.GetIdentifier()}
}

// refKey is the use of the subject ref by the subject parent, e.g. a backend used by a virtual server.
type refKey struct {
	ref    subjectKey
	parent subjectKey
}

func (a *Announcer) makeRefKey(subjectParent *observer.Subject, subjectRef *observer.Subject) refKey {
	return refKey{ref: a.makeKey(subjectRef), parent: a.makeKey(subjectParent)}
}

func (a *Announcer) addRef(subjectParent *observer.Subject, subjectRef *observer.Subject) {
	a.refMap.Store(a.makeRefKey(subjectParent, subjectRef), true)
}

func (a *Announcer) delAllRef(subject *observer.Subject) {
	subjKey := a.makeKey(subject)
	a.refMap.Range(func(key, value any) bool {
		ref := key.(refKey)
		if ref.ref == subjKey || ref.parent == subjKey {
			a.refMap.Delete(key)
		}
		return true
	})
}

func (a *Announcer) delRef(subjectParent *observer.Subject, subjectRef *observer.Subject) {
	a.refMap.Delete(a.makeRefKey(subjectParent, subjectRef))
}

func (a *Announcer) getNbRef(subject *observer.Subject) int32 {
	subjKey := a.makeKey(subject)
	var nb int32
	a.refMap.Range(func(key, value any) bool {
		if key.(refKey).ref == subjKey {
			nb++
		}
		return true
	})
	return nb
}

func (a *Announcer) updateSubject(
	ctx context.Context,
	session emitSessStore,
	eventType observer.EventType,
	subject *observer.Subject,
	reader func(string) (proto.Message, error),
	beforeStore func(current, new *svcdiscovery.Resource) error,
	beforeDelete func(current *svcdiscovery.Resource) error,
) {
	entry := a.entry.With().Str("subject_type", subject.GetTypeUrl()).Str("subject_identifier", subject.GetIdentifier()).Logger()

	entry.Debug().Msgf("Receive event %s", eventType.String())
	unlock := a.lockRes(subject)
	entry.Debug().Msg("Lock subject change")
	defer func() {
		unlock()
		entry.Debug().Msg("Unlock subject change")
	}()
	entry.Debug().Msgf("is referenced %d times.", a.getNbRef(subject))

	// we only want resource found at init or pre-registered by parent resource
	// othercases are subject for others instances
	currentResRaw, found := a.resMap.Load(a.makeKey(subject))
	if !found {
		return
	}

	if eventType == observer.EventType_DELETE_SUBJECT {
		// if resource is still used by other resources, we don't remove it
		if a.getNbRef(subject) > 0 {
			entry.Debug().Msgf("Resource is still used by other %d resources, skip remove", a.getNbRef(subject))
			return
		}
		if beforeDelete != nil {
			if err := beforeDelete(currentResRaw.(*svcdiscovery.Resource)); err != nil {
				a.entry.Error().Msgf("unable to delete subject: %s, error: %s", subject.GetIdentifier(), err.Error())
				return
			}
		}
		previousMsg, err := currentResRaw.(*svcdiscovery.Resource).StoredMessage()
		if err != nil {
			a.entry.Error().Msgf("unable to read deleted subject: %s, error: %s", subject.GetIdentifier(), err.Error())
			return
		}
		a.syncSecretRefs(ctx, session, subject, previousMsg, nil)
		entry.Debug().Msg("Remove resource")
		a.resMap.Delete(a.makeKey(subject))
		a.delAllRef(subject)
		session.addResource(RemoveEvent, subject.TypeUrl, currentResRaw.(*svcdiscovery.Resource))
		return
	}

	currentMsg, err := reader(subject.GetIdentifier())
	if ctx.Err() != nil {
		entry.Debug().Msg("announcer closed while fetching subject")
		return
	}
	if err != nil {
		a.entry.Error().Msgf("unable to fetch subject: %s, error: %s", subject.GetIdentifier(), err.Error())
		return
	}
	newRes, err := svcdiscovery.NewResourceFromMessage(currentMsg)
	if err != nil {
		a.entry.Error().Msgf("unable to convert subject to resource: %s, error: %s", subject.GetIdentifier(), err.Error())
		return
	}
	if currentResRaw.(*svcdiscovery.Resource).GetSignature() == newRes.GetSignature() {
		entry.Debug().Msg("Resource signature is the same, skip update")
		return
	}

	if beforeStore != nil {
		var currentRes *svcdiscovery.Resource
		if found {
			currentRes = currentResRaw.(*svcdiscovery.Resource)
		}
		if err := beforeStore(currentRes, newRes); err != nil {
			a.entry.Error().Msgf("unable to store subject: %s, error: %s", subject.GetIdentifier(), err.Error())
			return
		}
	}
	previousMsg, err := currentResRaw.(*svcdiscovery.Resource).StoredMessage()
	if err != nil {
		a.entry.Error().Msgf("unable to read stored subject: %s, error: %s", subject.GetIdentifier(), err.Error())
		return
	}
	a.syncSecretRefs(ctx, session, subject, previousMsg, currentMsg)
	entry.Debug().Msg("Store resource and emit event")
	a.resMap.Store(a.makeKey(subject), newRes)
	session.addResource(ChangeEvent, subject.GetTypeUrl(), newRes)
}

func (a *Announcer) updateBackend(ctx context.Context, session emitSessStore, eventType observer.EventType, subject *observer.Subject) {
	a.updateSubject(ctx, session, eventType, subject, func(s string) (proto.Message, error) {
		return a.sm.Backends().Read(ctx, s)
	}, nil, nil)
}

func (a *Announcer) updateCertificate(ctx context.Context, session emitSessStore, eventType observer.EventType, subject *observer.Subject) {
	a.updateSubject(ctx, session, eventType, subject, func(s string) (proto.Message, error) {
		return a.sm.Certificates().Read(ctx, s)
	}, nil, nil)
}

func (a *Announcer) updateSecret(ctx context.Context, session emitSessStore, eventType observer.EventType, subject *observer.Subject) {
	a.updateSubject(ctx, session, eventType, subject, func(s string) (proto.Message, error) {
		return a.sm.Secrets().Read(ctx, s)
	}, nil, nil)
}

func (a *Announcer) secretUuids(msg proto.Message) []string {
	refs, err := secret.FindRefs(msg)
	if err != nil {
		a.entry.Error().Msgf("ignoring the malformed secret references of a resource: %s", err.Error())
	}
	uuids := make([]string, 0, len(refs))
	for _, found := range refs {
		uuids = append(uuids, found.Ref.SecretUUID)
	}
	return lo.Uniq(uuids)
}

// syncSecretRefs makes the secrets referenced by the metadata and typed fields of parent follow the change from before
// to after, the way the certificates of a virtual server are followed. A nil before or after stands for a resource not
// there yet or gone.
func (a *Announcer) syncSecretRefs(ctx context.Context, session emitSessStore, parent *observer.Subject, before, after proto.Message) {
	remove, create := lo.Difference[string](a.secretUuids(before), a.secretUuids(after))
	for _, uuid := range remove {
		secretSubj := &observer.Subject{
			TypeUrl:    helpers.TypeUrl[*secret.Secret](),
			Identifier: uuid,
		}
		a.delRef(parent, secretSubj)
		a.updateSecret(ctx, session, observer.EventType_DELETE_SUBJECT, secretSubj)
	}
	for _, uuid := range create {
		secretSubj := &observer.Subject{
			TypeUrl:    helpers.TypeUrl[*secret.Secret](),
			Identifier: uuid,
		}
		a.resMap.LoadOrStore(a.makeKey(secretSubj), &svcdiscovery.Resource{
			Uuid: uuid,
		})
		a.addRef(parent, secretSubj)
		a.updateSecret(ctx, session, observer.EventType_CREATE_SUBJECT, secretSubj)
	}
}

func (a *Announcer) updateInstance(ctx context.Context, session emitSessStore, eventType observer.EventType, subject *observer.Subject) {
	if eventType != observer.EventType_UPDATE_SUBJECT || subject.GetIdentifier() != a.instanceUuid {
		return
	}

	currentInstance, err := a.sm.Instances().Read(ctx, subject.GetIdentifier())
	if ctx.Err() != nil {
		a.entry.Debug().Msg("announcer closed while fetching instance")
		return
	}
	if err != nil {
		a.entry.Error().Msgf("unable to fetch instance: %s, error: %s", subject.GetIdentifier(), err.Error())
		return
	}

	remove, create := lo.Difference[string](a.instance.GetVipsUuid(), currentInstance.GetVipsUuid())
	for _, uuid := range remove {
		// there is only one instance on announcer so we can have only one copy of a vip on an instance
		// we don't need to update reference
		a.updateVip(ctx, session, observer.EventType_DELETE_SUBJECT, &observer.Subject{
			TypeUrl:    helpers.TypeUrl[*vip.VIP](),
			Identifier: uuid,
		})
	}
	for _, uuid := range create {
		vipSubj := &observer.Subject{
			TypeUrl:    helpers.TypeUrl[*vip.VIP](),
			Identifier: uuid,
		}
		a.resMap.LoadOrStore(a.makeKey(vipSubj), &svcdiscovery.Resource{
			Uuid: uuid,
		})
		a.updateVip(ctx, session, observer.EventType_CREATE_SUBJECT, vipSubj)
	}
	instanceSubj := &observer.Subject{
		TypeUrl:    helpers.TypeUrl[*instance.Instance](),
		Identifier: a.instanceUuid,
	}
	a.syncSecretRefs(ctx, session, instanceSubj, a.instance, currentInstance)
	a.instance = currentInstance
}

func (a *Announcer) updateVip(ctx context.Context, session emitSessStore, eventType observer.EventType, subject *observer.Subject) {
	a.updateSubject(ctx, session, eventType, subject,
		func(s string) (proto.Message, error) {
			return a.sm.VIPs().Read(ctx, s)
		},
		func(current, new *svcdiscovery.Resource) error {
			curVip := &vip.VIP{}
			err := current.ToMessage(curVip)
			if err != nil {
				return err
			}
			newVip := &vip.VIP{}
			err = new.ToMessage(newVip)
			if err != nil {
				return err
			}
			remove, create := lo.Difference[string](curVip.GetVirtualServersUuid(), newVip.GetVirtualServersUuid())
			for _, uuid := range remove {
				vsSubj := &observer.Subject{
					TypeUrl:    helpers.TypeUrl[*vs.VirtualServer](),
					Identifier: uuid,
				}
				a.delRef(subject, vsSubj)
				a.updateVirtualServer(ctx, session, observer.EventType_DELETE_SUBJECT, vsSubj)
			}
			for _, uuid := range create {
				vsSubj := &observer.Subject{
					TypeUrl:    helpers.TypeUrl[*vs.VirtualServer](),
					Identifier: uuid,
				}
				a.resMap.LoadOrStore(a.makeKey(vsSubj), &svcdiscovery.Resource{
					Uuid: uuid,
				})
				a.addRef(subject, vsSubj)
				a.updateVirtualServer(ctx, session, observer.EventType_CREATE_SUBJECT, vsSubj)
			}
			return nil
		},
		func(current *svcdiscovery.Resource) error {
			curVip := &vip.VIP{}
			err := current.ToMessage(curVip)
			if err != nil {
				return err
			}
			for _, uuid := range curVip.GetVirtualServersUuid() {
				vsSubj := &observer.Subject{
					TypeUrl:    helpers.TypeUrl[*vs.VirtualServer](),
					Identifier: uuid,
				}
				a.delRef(subject, vsSubj)
				a.updateVirtualServer(ctx, session, observer.EventType_DELETE_SUBJECT, vsSubj)
			}
			return nil
		},
	)
}

func (a *Announcer) updateVirtualServer(ctx context.Context, session emitSessStore, eventType observer.EventType, subject *observer.Subject) {
	a.updateSubject(ctx, session, eventType, subject,
		func(s string) (proto.Message, error) {
			return a.sm.VirtualServers().Read(ctx, s)
		},
		func(current, new *svcdiscovery.Resource) error {

			curVs := &vs.VirtualServer{}
			err := current.ToMessage(curVs)
			if err != nil {
				return err
			}
			newVs := &vs.VirtualServer{}
			err = new.ToMessage(newVs)
			if err != nil {
				return err
			}

			remove, create := lo.Difference[string](curVs.GetBackendUuids(), newVs.GetBackendUuids())
			for _, uuid := range remove {
				backendSubj := &observer.Subject{
					TypeUrl:    helpers.TypeUrl[*backend.Backend](),
					Identifier: uuid,
				}
				a.delRef(subject, backendSubj)
				a.updateBackend(ctx, session, observer.EventType_DELETE_SUBJECT, backendSubj)
			}
			for _, uuid := range create {
				backendSubj := &observer.Subject{
					TypeUrl:    helpers.TypeUrl[*backend.Backend](),
					Identifier: uuid,
				}
				a.resMap.LoadOrStore(a.makeKey(backendSubj), &svcdiscovery.Resource{
					Uuid: uuid,
				})
				a.addRef(subject, backendSubj)
				a.updateBackend(ctx, session, observer.EventType_CREATE_SUBJECT, backendSubj)
			}

			remove, create = lo.Difference[string](curVs.GetCertificatesUuids(), newVs.GetCertificatesUuids())
			for _, uuid := range remove {
				certSubj := &observer.Subject{
					TypeUrl:    helpers.TypeUrl[*cert.Certificate](),
					Identifier: uuid,
				}
				a.delRef(subject, certSubj)
				a.updateCertificate(ctx, session, observer.EventType_DELETE_SUBJECT, certSubj)
			}
			for _, uuid := range create {
				certSubj := &observer.Subject{
					TypeUrl:    helpers.TypeUrl[*cert.Certificate](),
					Identifier: uuid,
				}
				a.resMap.LoadOrStore(a.makeKey(certSubj), &svcdiscovery.Resource{
					Uuid: uuid,
				})
				a.addRef(subject, certSubj)
				a.updateCertificate(ctx, session, observer.EventType_CREATE_SUBJECT, certSubj)
			}
			return nil
		},
		func(current *svcdiscovery.Resource) error {
			curVs := &vs.VirtualServer{}
			err := current.ToMessage(curVs)
			if err != nil {
				return err
			}

			for _, uuid := range curVs.GetBackendUuids() {
				backendSubj := &observer.Subject{
					TypeUrl:    helpers.TypeUrl[*backend.Backend](),
					Identifier: uuid,
				}
				a.delRef(subject, backendSubj)
				a.updateBackend(ctx, session, observer.EventType_DELETE_SUBJECT, backendSubj)
			}

			for _, uuid := range curVs.GetCertificatesUuids() {
				certSubj := &observer.Subject{
					TypeUrl:    helpers.TypeUrl[*cert.Certificate](),
					Identifier: uuid,
				}
				a.delRef(subject, certSubj)
				a.updateCertificate(ctx, session, observer.EventType_DELETE_SUBJECT, certSubj)
			}

			return nil
		},
	)
}

func (a *Announcer) emitSession(session emitSessStore) {
	for topic, resources := range session {
		if zerolog.GlobalLevel() <= zerolog.DebugLevel {
			for _, res := range resources {
				a.entry.Debug().
					Str("topic", topic).
					Str("res_uuid", res.Uuid).
					Str("res_type", res.Resource.GetTypeUrl()).
					Msg("Emitting")
			}
		}
		a.emit.Emit(emitter.NewEventOf[[]*svcdiscovery.Resource](topic, resources))
	}
}

func (a *Announcer) LockReceive() {
	a.rwMutex.Lock()
}

func (a *Announcer) UnlockReceive() {
	a.rwMutex.Unlock()
}

// SnapRessources gives what a listener of typeUrl must receive to catch up from initResVers (the uuids and
// signatures it already has), limited to the resources matching filters.
func (a *Announcer) SnapRessources(
	typeUrl string,
	initResVers map[string]string,
	filters ...*svccore.ListFilter,
) (changes []*svcdiscovery.Resource, removes []string) {
	if initResVers == nil {
		initResVers = make(map[string]string)
	}
	resources := make(map[string]*svcdiscovery.Resource)
	a.resMap.Range(func(key, value any) bool {
		subjKey := key.(subjectKey)
		if subjKey.typeUrl != typeUrl {
			return true
		}
		res := value.(*svcdiscovery.Resource)
		// a placeholder, not read from the store yet, has nothing to send
		isPlaceholder := res.GetResource() == nil
		if isPlaceholder || !matchFilters(filters, res) {
			return true
		}
		resources[subjKey.identifier] = res
		return true
	})

	toRemoves := make([]string, 0)
	for uuid := range initResVers {
		_, ok := resources[uuid]
		if !ok {
			toRemoves = append(toRemoves, uuid)
		}
	}

	for uuid, res := range resources {
		initSig, ok := initResVers[uuid]
		if !ok {
			continue
		}
		if initSig == res.GetSignature() {
			delete(resources, uuid)
		}
	}

	finalRes := make([]*svcdiscovery.Resource, len(resources))
	i := 0
	for _, res := range resources {
		finalRes[i] = res
		i++
	}
	return finalRes, toRemoves
}

// On makes aListen receive the changes of the resources of typeUrl matching filters. The resources matching
// them now are the ones SnapRessources gave it.
func (a *Announcer) On(typeUrl string, aListen Listener, filters ...*svccore.ListFilter) {
	sent := make(map[string]bool)
	a.resMap.Range(func(key, value any) bool {
		res := value.(*svcdiscovery.Resource)
		isOfType := key.(subjectKey).typeUrl == typeUrl
		if isOfType && res.GetResource() != nil && matchFilters(filters, res) {
			sent[res.GetUuid()] = true
		}
		return true
	})
	current := func(uuid string) (*svcdiscovery.Resource, bool) {
		raw, found := a.resMap.Load(a.makeKey(&observer.Subject{TypeUrl: typeUrl, Identifier: uuid}))
		if !found {
			return nil, false
		}
		res := raw.(*svcdiscovery.Resource)
		// a placeholder, not read from the store yet, has nothing to send
		isPlaceholder := res.GetResource() == nil
		return res, !isPlaceholder
	}
	listChange := NewChangeListener(aListen, filters, sent, current)
	a.listenersMap.Store(fmt.Sprintf("%p", aListen), listChange)
	a.emit.On(makeAnnounceTopic("*", typeUrl), listChange)
}

func (a *Announcer) Off(typeUrl string, aListens ...Listener) {
	listeners := make([]emitter.Listener, 0)
	for _, aListen := range aListens {
		ptrName := fmt.Sprintf("%p", aListen)
		listener, ok := a.listenersMap.Load(ptrName)
		if !ok {
			continue
		}
		listeners = append(listeners, listener.(emitter.Listener))
		a.listenersMap.Delete(ptrName)
	}
	a.emit.Off(makeAnnounceTopic("*", typeUrl), listeners...)
}

func (a *Announcer) Close() {
	a.cancel()
	a.emit.Off("*")
	a.listenersMap.Range(func(key, value any) bool {
		a.listenersMap.Delete(key)
		return true
	})
}

func makeAnnounceTopic(evName EventName, typeUrl string) string {
	typeUrl = strings.Replace(typeUrl, "/", ".", -1)
	return fmt.Sprintf("%s.%s", evName, typeUrl)
}
