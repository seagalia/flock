package manager

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	"connectrpc.com/connect/v2"
	backendv1 "github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1/svcbackendconnect"
	"github.com/seagalia/flock/sdk/flock/services/cert/v1"
	"github.com/seagalia/flock/sdk/flock/services/cert/v1/svccertconnect"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
	"github.com/seagalia/flock/sdk/storers"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

type storable interface {
	proto.Message
	GetUUID() string
	GetName() string
	GetMetadata() map[string]string
}

// fakeStore keeps the resources of one kind like the controller does: unique names, filters on metadata ANDed and on
// name or uuid ORed
type fakeStore[T storable] struct {
	kind    string
	items   map[string]T
	setUuid func(T, string)
}

func newFakeStore[T storable](kind string, setUuid func(T, string)) *fakeStore[T] {
	return &fakeStore[T]{kind: kind, items: map[string]T{}, setUuid: setUuid}
}

func (s *fakeStore[T]) put(item T) T {
	stored := proto.CloneOf(item)
	if stored.GetUUID() == "" {
		s.setUuid(stored, fmt.Sprintf("%s-%d", s.kind, len(s.items)+1))
	}
	s.items[stored.GetUUID()] = stored
	return proto.CloneOf(stored)
}

func (s *fakeStore[T]) create(item T) (T, error) {
	var zero T
	for _, existing := range s.items {
		if existing.GetName() == item.GetName() {
			return zero, connect.Errorf(connect.CodeAlreadyExists, "%s %s exists", s.kind, item.GetName())
		}
	}
	return s.put(item), nil
}

func (s *fakeStore[T]) update(uuid string, item T) (T, error) {
	var zero T
	if _, exists := s.items[uuid]; !exists {
		return zero, connect.Errorf(connect.CodeNotFound, "%s %s not found", s.kind, uuid)
	}
	updated := proto.CloneOf(item)
	s.setUuid(updated, uuid)
	return s.put(updated), nil
}

func (s *fakeStore[T]) read(uuid string) (T, error) {
	item, exists := s.items[uuid]
	if !exists {
		var zero T
		return zero, connect.Errorf(connect.CodeNotFound, "%s %s not found", s.kind, uuid)
	}
	return proto.CloneOf(item), nil
}

func (s *fakeStore[T]) list(offset, limit int32, filters []*svccore.ListFilter) []T {
	var matching []T
	for _, uuid := range slices.Sorted(maps.Keys(s.items)) {
		if matchFilters(s.items[uuid], filters) {
			matching = append(matching, proto.CloneOf(s.items[uuid]))
		}
	}
	if int(offset) >= len(matching) {
		return nil
	}
	return matching[offset:min(int(offset+limit), len(matching))]
}

func (s *fakeStore[T]) all() []T {
	return s.list(0, int32(len(s.items)), nil)
}

func (s *fakeStore[T]) byName(name string) T {
	var zero T
	for _, item := range s.items {
		if item.GetName() == name {
			return proto.CloneOf(item)
		}
	}
	return zero
}

func matchFilters(item storable, filters []*svccore.ListFilter) bool {
	hasRefFilter := false
	matchesRef := false
	for _, filter := range filters {
		switch {
		case filter.GetIsMetadata():
			if item.GetMetadata()[filter.GetKey()] != filter.GetValue() {
				return false
			}
		case filter.GetKey() == storers.FilterNameKey:
			hasRefFilter = true
			matchesRef = matchesRef || item.GetName() == filter.GetValue()
		case filter.GetKey() == storers.FilterUuidKey:
			hasRefFilter = true
			matchesRef = matchesRef || item.GetUUID() == filter.GetValue()
		}
	}
	return !hasRefFilter || matchesRef
}

type fakeController struct {
	svcbackendconnect.BackendClient
	svccertconnect.CertificateClient
	svcvsconnect.VirtualServerClient
	svcvipconnect.VIPClient

	mu       sync.Mutex
	backends *fakeStore[*backendv1.Backend]
	certs    *fakeStore[*cert.Certificate]
	vss      *fakeStore[*vs.VirtualServer]
	vips     *fakeStore[*vip.VIP]
	writes   int
	// backendLists counts the lists of the owned resources, the only ones listing backends
	backendLists int
}

func newFakeController() *fakeController {
	return &fakeController{
		backends: newFakeStore("backend", func(b *backendv1.Backend, uuid string) { b.UUID = uuid }),
		certs:    newFakeStore("cert", func(c *cert.Certificate, uuid string) { c.UUID = uuid }),
		vss:      newFakeStore("vs", func(v *vs.VirtualServer, uuid string) { v.UUID = uuid }),
		vips:     newFakeStore("vip", func(v *vip.VIP, uuid string) { v.UUID = uuid }),
	}
}

func (f *fakeController) clients() ControllerClients {
	return ControllerClients{Backends: f, Certs: f, Vss: f, Vips: f}
}

func (f *fakeController) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes
}

func (f *fakeController) CreateBackend(_ context.Context, req *svcbackend.CreateBackendRequest) (*svcbackend.BackendResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	created, err := f.backends.create(req.GetBackend())
	return &svcbackend.BackendResponse{Backend: created}, err
}

func (f *fakeController) UpdateBackend(_ context.Context, req *svcbackend.UpdateBackendRequest) (*svcbackend.BackendResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	updated, err := f.backends.update(req.GetUUID(), req.GetBackend())
	return &svcbackend.BackendResponse{Backend: updated}, err
}

// DeleteBackend refuses a backend a virtual server still routes to, as the controller does
func (f *fakeController) DeleteBackend(_ context.Context, req *svccore.UuidRequest) (*emptypb.Empty, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	for _, virtualServer := range f.vss.items {
		if usesBackend(virtualServer, req.GetUUID()) {
			return nil, connect.Errorf(connect.CodeFailedPrecondition, "used by %s", virtualServer.GetName())
		}
	}
	delete(f.backends.items, req.GetUUID())
	return &emptypb.Empty{}, nil
}

func usesBackend(virtualServer *vs.VirtualServer, backendUuid string) bool {
	if virtualServer.GetTcpRouteConfig().GetBackendUuid() == backendUuid {
		return true
	}
	if virtualServer.GetUdpRouteConfig().GetBackendUuid() == backendUuid {
		return true
	}
	for _, virtualHost := range virtualServer.GetHttpRoutesConfig().GetVirtualHosts() {
		for _, r := range virtualHost.GetRoutes() {
			if r.GetBackendUuid() == backendUuid {
				return true
			}
		}
	}
	for _, policy := range virtualServer.GetHttpRoutesConfig().GetExternalAuths() {
		if policy.GetBackendUuid() == backendUuid {
			return true
		}
	}
	return false
}

// checkExternalAuths refuses a route naming a policy the virtual server doesn't define, as the controller does
func checkExternalAuths(virtualServer *vs.VirtualServer) error {
	httpConfig := virtualServer.GetHttpRoutesConfig()
	for _, virtualHost := range httpConfig.GetVirtualHosts() {
		for _, r := range virtualHost.GetRoutes() {
			policy := r.GetExternalAuthName()
			isDefined := slices.ContainsFunc(httpConfig.GetExternalAuths(), func(defined *route.ExternalAuth) bool {
				return defined.GetName() == policy
			})
			if policy != "" && !isDefined {
				return connect.Errorf(connect.CodeInvalidArgument, "external auth %s is not defined", policy)
			}
		}
	}
	return nil
}

func (f *fakeController) ownedListCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.backendLists
}

func (f *fakeController) ListBackend(_ context.Context, req *svcbackend.ListBackendRequest) (*svcbackend.ListBackendResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if req.GetOffset() == 0 {
		f.backendLists++
	}
	return &svcbackend.ListBackendResponse{Backends: f.backends.list(req.GetOffset(), req.GetLimit(), req.GetFilters())}, nil
}

func (f *fakeController) CreateCertificate(_ context.Context, req *svccert.CreateCertificateRequest) (*svccert.CertificateResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	created, err := f.certs.create(req.GetCertificate())
	return &svccert.CertificateResponse{Certificate: created}, err
}

func (f *fakeController) ReadCertificate(_ context.Context, req *svccore.UuidRequest) (*svccert.CertificateResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	found, err := f.certs.read(req.GetUUID())
	return &svccert.CertificateResponse{Certificate: found}, err
}

func (f *fakeController) UpdateCertificate(_ context.Context, req *svccert.UpdateCertificateRequest) (*svccert.CertificateResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	updated, err := f.certs.update(req.GetUUID(), req.GetCertificate())
	return &svccert.CertificateResponse{Certificate: updated}, err
}

func (f *fakeController) ListCertificate(_ context.Context, req *svccert.ListCertificateRequest) (*svccert.ListCertificateResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &svccert.ListCertificateResponse{Certificates: f.certs.list(req.GetOffset(), req.GetLimit(), req.GetFilters())}, nil
}

func (f *fakeController) CreateVS(_ context.Context, req *svcvs.CreateVSRequest) (*svcvs.VSResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	err := checkExternalAuths(req.GetVs())
	if err != nil {
		return nil, err
	}
	created, err := f.vss.create(req.GetVs())
	return &svcvs.VSResponse{Vs: created}, err
}

func (f *fakeController) UpdateVS(_ context.Context, req *svcvs.UpdateVSRequest) (*svcvs.VSResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	err := checkExternalAuths(req.GetVs())
	if err != nil {
		return nil, err
	}
	updated, err := f.vss.update(req.GetUUID(), req.GetVs())
	return &svcvs.VSResponse{Vs: updated}, err
}

// DeleteVS refuses a virtual server a vip still holds, as the controller does
func (f *fakeController) DeleteVS(_ context.Context, req *svccore.UuidRequest) (*emptypb.Empty, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	for _, holder := range f.vips.items {
		if slices.Contains(holder.GetVirtualServersUuid(), req.GetUUID()) {
			return nil, connect.Errorf(connect.CodeFailedPrecondition, "on vip %s", holder.GetName())
		}
	}
	delete(f.vss.items, req.GetUUID())
	return &emptypb.Empty{}, nil
}

func (f *fakeController) ListVS(_ context.Context, req *svcvs.ListVSRequest) (*svcvs.ListVSResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &svcvs.ListVSResponse{Vs: f.vss.list(req.GetOffset(), req.GetLimit(), req.GetFilters())}, nil
}

func (f *fakeController) UpdateVIP(_ context.Context, req *svcvip.UpdateVIPRequest) (*svcvip.VIPResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	updated, err := f.vips.update(req.GetUUID(), req.GetVip())
	return &svcvip.VIPResponse{Vip: updated}, err
}

func (f *fakeController) ListVIP(_ context.Context, req *svcvip.ListVIPRequest) (*svcvip.ListVIPResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &svcvip.ListVIPResponse{Vip: f.vips.list(req.GetOffset(), req.GetLimit(), req.GetFilters())}, nil
}
