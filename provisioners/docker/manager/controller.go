package manager

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/seagalia/flock/provisioners/config"
	backendv1 "github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1"
	"github.com/seagalia/flock/sdk/flock/services/cert/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/storers"
	"google.golang.org/protobuf/proto"
)

const (
	controllerCallTimeout = 30 * time.Second
	pageSize              = 100
)

var errNameTaken = errors.New("the name is taken by a resource the docker provisioner doesn't own")

// owned is what the provisioner wrote on the controller, by name
type owned struct {
	backends map[string]*backendv1.Backend
	certs    map[string]*cert.Certificate
	vss      map[string]*vs.VirtualServer
}

func (m *Manager) ownedFilters() svccore.ListFilters {
	dockerKey, dockerValue := config.MakeProvisionerMetadata(config.DockerProvisionerName)
	return svccore.ListFilters{
		{Key: dockerKey, Value: dockerValue, Operator: svccore.ListFilter_EQUAL, IsMetadata: true},
		{Key: helpers.MetadataProvisionBy, Value: m.agentUuid, Operator: svccore.ListFilter_EQUAL, IsMetadata: true},
	}
}

// refFilters match a resource by uuid or by name, the controller ORs them
func refFilters(ref string) svccore.ListFilters {
	return svccore.ListFilters{
		{Key: storers.FilterUuidKey, Value: ref, Operator: svccore.ListFilter_EQUAL},
		{Key: storers.FilterNameKey, Value: ref, Operator: svccore.ListFilter_EQUAL},
	}
}

func (m *Manager) ownedResources(ctx context.Context) (*owned, error) {
	if m.owned != nil {
		return m.owned, nil
	}
	listed, err := m.listOwned(ctx)
	if err != nil {
		return nil, err
	}
	m.owned = listed
	return listed, nil
}

// forgetOwned drops the cache on a failed write: the write may still have happened, or a user changed what it
// conflicts with, so the next rebuild lists again.
func (m *Manager) forgetOwned(err error) error {
	m.owned = nil
	return err
}

func (m *Manager) isOwned(metadata map[string]string) bool {
	dockerKey, dockerValue := config.MakeProvisionerMetadata(config.DockerProvisionerName)
	return metadata[dockerKey] == dockerValue && metadata[helpers.MetadataProvisionBy] == m.agentUuid
}

func (m *Manager) listOwned(ctx context.Context) (*owned, error) {
	filters := m.ownedFilters()
	backends, err := listAll(ctx, func(ctx context.Context, offset int32) ([]*backendv1.Backend, error) {
		resp, err := m.clients.Backends.ListBackend(ctx, &svcbackend.ListBackendRequest{
			Limit: pageSize, Offset: offset, Filters: filters,
		})
		return resp.GetBackends(), err
	})
	if err != nil {
		return nil, fmt.Errorf("list owned backends: %w", err)
	}
	certs, err := listAll(ctx, func(ctx context.Context, offset int32) ([]*cert.Certificate, error) {
		resp, err := m.clients.Certs.ListCertificate(ctx, &svccert.ListCertificateRequest{
			Limit: pageSize, Offset: offset, Filters: filters,
		})
		return resp.GetCertificates(), err
	})
	if err != nil {
		return nil, fmt.Errorf("list owned certificates: %w", err)
	}
	vss, err := listAll(ctx, func(ctx context.Context, offset int32) ([]*vs.VirtualServer, error) {
		resp, err := m.clients.Vss.ListVS(ctx, &svcvs.ListVSRequest{Limit: pageSize, Offset: offset, Filters: filters})
		return resp.GetVs(), err
	})
	if err != nil {
		return nil, fmt.Errorf("list owned virtual servers: %w", err)
	}
	return &owned{backends: byName(backends), certs: byName(certs), vss: byName(vss)}, nil
}

func listAll[T proto.Message](ctx context.Context, listPage func(context.Context, int32) ([]T, error)) ([]T, error) {
	var all []T
	for offset := int32(0); ; offset += pageSize {
		callCtx, cancel := context.WithTimeout(ctx, controllerCallTimeout)
		page, err := listPage(callCtx, offset)
		cancel()
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return all, nil
		}
		all = append(all, page...)
	}
}

func byName[T proto.Message](resources []T) map[string]T {
	named := make(map[string]T, len(resources))
	for _, resource := range resources {
		named[helpers.GetName(resource)] = resource
	}
	return named
}

func (m *Manager) findVs(ctx context.Context, ref string) (*vs.VirtualServer, error) {
	callCtx, cancel := context.WithTimeout(ctx, controllerCallTimeout)
	defer cancel()
	resp, err := m.clients.Vss.ListVS(callCtx, &svcvs.ListVSRequest{Limit: 2, Filters: refFilters(ref)})
	if err != nil {
		return nil, fmt.Errorf("find virtual server %s: %w", ref, err)
	}
	return onlyOne("virtual server", ref, resp.GetVs())
}

func (m *Manager) findCertUuid(ctx context.Context, ref string) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, controllerCallTimeout)
	defer cancel()
	resp, err := m.clients.Certs.ListCertificate(callCtx, &svccert.ListCertificateRequest{
		Limit: 2, Filters: refFilters(ref),
	})
	if err != nil {
		return "", fmt.Errorf("find certificate %s: %w", ref, err)
	}
	found, err := onlyOne("certificate", ref, resp.GetCertificates())
	if err != nil {
		return "", err
	}
	if found == nil {
		return "", fmt.Errorf("certificate %s doesn't exist", ref)
	}
	return found.GetUUID(), nil
}

func (m *Manager) findVip(ctx context.Context, name string) (*vip.VIP, error) {
	callCtx, cancel := context.WithTimeout(ctx, controllerCallTimeout)
	defer cancel()
	resp, err := m.clients.Vips.ListVIP(callCtx, &svcvip.ListVIPRequest{
		Limit:   2,
		Filters: svccore.ListFilters{{Key: storers.FilterNameKey, Value: name, Operator: svccore.ListFilter_EQUAL}},
	})
	if err != nil {
		return nil, fmt.Errorf("find vip %s: %w", name, err)
	}
	found, err := onlyOne("vip", name, resp.GetVip())
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, fmt.Errorf("vip %s doesn't exist", name)
	}
	return found, nil
}

// onlyOne refuses a ref matching several resources: names are not unique, and a name can be another one's uuid
func onlyOne[T any](kind, ref string, found []*T) (*T, error) {
	switch len(found) {
	case 0:
		return nil, nil
	case 1:
		return found[0], nil
	default:
		return nil, fmt.Errorf("%s %s matches several resources, use its uuid", kind, ref)
	}
}

// writeBackend creates desired, or updates stored when it differs, and answers the uuid
func (m *Manager) writeBackend(ctx context.Context, desired, stored *backendv1.Backend) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, controllerCallTimeout)
	defer cancel()
	if stored == nil {
		resp, err := m.clients.Backends.CreateBackend(callCtx, &svcbackend.CreateBackendRequest{Backend: desired})
		if err != nil {
			return "", m.forgetOwned(createError("backend", desired.GetName(), err))
		}
		m.cacheBackend(resp.GetBackend())
		return resp.GetBackend().GetUUID(), nil
	}
	desired.UUID = stored.GetUUID()
	if sameResource(stored, desired) {
		return stored.GetUUID(), nil
	}
	resp, err := m.clients.Backends.UpdateBackend(callCtx, &svcbackend.UpdateBackendRequest{
		UUID: desired.GetUUID(), Backend: desired,
	})
	if err != nil {
		return "", m.forgetOwned(fmt.Errorf("update backend %s: %w", desired.GetName(), err))
	}
	m.cacheBackend(resp.GetBackend())
	return stored.GetUUID(), nil
}

func (m *Manager) cacheBackend(written *backendv1.Backend) {
	if m.owned != nil {
		m.owned.backends[written.GetName()] = written
	}
}

// writeVs creates desired, or updates stored when it differs, and answers the uuid
func (m *Manager) writeVs(ctx context.Context, desired, stored *vs.VirtualServer) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, controllerCallTimeout)
	defer cancel()
	if stored == nil {
		resp, err := m.clients.Vss.CreateVS(callCtx, &svcvs.CreateVSRequest{Vs: desired})
		if err != nil {
			return "", m.forgetOwned(createError("virtual server", desired.GetName(), err))
		}
		m.cacheVs(resp.GetVs())
		return resp.GetVs().GetUUID(), nil
	}
	desired.UUID = stored.GetUUID()
	if sameResource(stored, desired) {
		return stored.GetUUID(), nil
	}
	resp, err := m.clients.Vss.UpdateVS(callCtx, &svcvs.UpdateVSRequest{UUID: desired.GetUUID(), Vs: desired})
	if err != nil {
		return "", m.forgetOwned(fmt.Errorf("update virtual server %s: %w", desired.GetName(), err))
	}
	m.cacheVs(resp.GetVs())
	return stored.GetUUID(), nil
}

// cacheVs leaves out the virtual servers of users some container joined, the cache only holds the owned ones
func (m *Manager) cacheVs(written *vs.VirtualServer) {
	if m.owned != nil && m.isOwned(written.GetMetadata()) {
		m.owned.vss[written.GetName()] = written
	}
}

// writeCertificate creates desired when missing. An owned one only follows a change of domains, starting from the copy
// read right before: the acme provisioner writes its own keys in the metadata, which a whole replace would drop.
func (m *Manager) writeCertificate(ctx context.Context, desired, stored *cert.Certificate) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, controllerCallTimeout)
	defer cancel()
	if stored == nil {
		resp, err := m.clients.Certs.CreateCertificate(callCtx, &svccert.CreateCertificateRequest{Certificate: desired})
		if err != nil {
			return "", m.forgetOwned(createError("certificate", desired.GetName(), err))
		}
		m.cacheCertificate(resp.GetCertificate())
		return resp.GetCertificate().GetUUID(), nil
	}
	if slices.Equal(stored.GetDomainNames(), desired.GetDomainNames()) {
		return stored.GetUUID(), nil
	}
	latest, err := m.clients.Certs.ReadCertificate(callCtx, &svccore.UuidRequest{UUID: stored.GetUUID()})
	if err != nil {
		return "", m.forgetOwned(fmt.Errorf("read certificate %s: %w", desired.GetName(), err))
	}
	updated := latest.GetCertificate()
	updated.DomainNames = desired.GetDomainNames()
	resp, err := m.clients.Certs.UpdateCertificate(callCtx, &svccert.UpdateCertificateRequest{
		UUID: updated.GetUUID(), Certificate: updated,
	})
	if err != nil {
		return "", m.forgetOwned(fmt.Errorf("update certificate %s: %w", desired.GetName(), err))
	}
	m.cacheCertificate(resp.GetCertificate())
	return updated.GetUUID(), nil
}

func (m *Manager) cacheCertificate(written *cert.Certificate) {
	if m.owned != nil {
		m.owned.certs[written.GetName()] = written
	}
}

func (m *Manager) deleteVs(ctx context.Context, stored *vs.VirtualServer) error {
	callCtx, cancel := context.WithTimeout(ctx, controllerCallTimeout)
	defer cancel()
	_, err := m.clients.Vss.DeleteVS(callCtx, &svccore.UuidRequest{UUID: stored.GetUUID()})
	if err != nil && connect.CodeOf(err) != connect.CodeNotFound {
		return m.forgetOwned(fmt.Errorf("delete virtual server %s: %w", stored.GetName(), err))
	}
	if m.owned != nil {
		delete(m.owned.vss, stored.GetName())
	}
	return nil
}

func (m *Manager) deleteBackend(ctx context.Context, stored *backendv1.Backend) error {
	callCtx, cancel := context.WithTimeout(ctx, controllerCallTimeout)
	defer cancel()
	_, err := m.clients.Backends.DeleteBackend(callCtx, &svccore.UuidRequest{UUID: stored.GetUUID()})
	if err != nil && connect.CodeOf(err) != connect.CodeNotFound {
		return m.forgetOwned(fmt.Errorf("delete backend %s: %w", stored.GetName(), err))
	}
	if m.owned != nil {
		delete(m.owned.backends, stored.GetName())
	}
	return nil
}

func (m *Manager) updateVip(ctx context.Context, updated *vip.VIP) error {
	callCtx, cancel := context.WithTimeout(ctx, controllerCallTimeout)
	defer cancel()
	_, err := m.clients.Vips.UpdateVIP(callCtx, &svcvip.UpdateVIPRequest{UUID: updated.GetUUID(), Vip: updated})
	if err != nil {
		return fmt.Errorf("update vip %s: %w", updated.GetName(), err)
	}
	return nil
}

func createError(kind, name string, err error) error {
	if connect.CodeOf(err) == connect.CodeAlreadyExists {
		return fmt.Errorf("create %s %s: %w", kind, name, errNameTaken)
	}
	return fmt.Errorf("create %s %s: %w", kind, name, err)
}

func sameResource(stored, desired proto.Message) bool {
	stored, desired = proto.Clone(stored), proto.Clone(desired)
	helpers.ProvDefault(stored)
	helpers.ProvDefault(desired)
	return proto.Equal(stored, desired)
}
