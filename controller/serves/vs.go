package serves

import (
	"context"
	"slices"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/api/observer/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/observe"
	"google.golang.org/protobuf/types/known/emptypb"
)

func (s *FlockServer) CreateVS(ctx context.Context, req *svcvs.CreateVSRequest) (*svcvs.VSResponse, error) {
	virtServ := req.GetVs()
	virtServ.UUID = uuid.New().String()

	sig, err := helpers.MessageSignature(virtServ)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "could not create signature: %v", err).WithCause(err)
	}

	err = s.checkVsRequest(ctx, virtServ)
	if err != nil {
		return nil, err
	}

	err = s.checkSecretRefs(ctx, nil, virtServ)
	if err != nil {
		return nil, err
	}

	err = s.storeMan.VirtualServers().Create(ctx, virtServ)
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	// the virtual server is stored: what follows completes the change, it must not stop halfway when the client leaves
	ctx, cancel := detachedContext(ctx)
	defer cancel()

	err = s.addPermForUser(ctx, helpers.TypeUrl[*vs.VirtualServer](), virtServ.UUID)
	if err != nil {
		return nil, err
	}

	observe.EmitSubject(observer.EventType_CREATE_SUBJECT, &observer.Subject{
		TypeUrl:    helpers.TypeUrlFromMessage(virtServ),
		Identifier: virtServ.UUID,
		Signature:  sig,
	})

	return &svcvs.VSResponse{
		Vs: virtServ,
	}, nil
}

func (s *FlockServer) checkVsRequest(ctx context.Context, virtServ *vs.VirtualServer) error {
	err := checkExternalAuths(virtServ.GetHttpRoutesConfig())
	if err != nil {
		return err
	}

	// envoy refuses the whole listener when two virtual hosts share a domain
	domainOwner := make(map[string]int)
	for i, vhost := range virtServ.GetHttpRoutesConfig().GetVirtualHosts() {
		for _, domain := range vhost.GetDomains() {
			previous, used := domainOwner[domain]
			if used {
				return connect.Errorf(connect.CodeInvalidArgument,
					"domain %s is in virtual hosts %d and %d, a domain can be in one virtual host only", domain, previous, i,
				)
			}
			domainOwner[domain] = i
		}
	}

	backendUuids := virtServ.GetBackendUuids()
	for _, backendUuid := range backendUuids {
		exists, err := s.storeMan.Backends().Exists(ctx, backendUuid)
		if err != nil {
			return connect.Errorf(connect.CodeInternal, "could not find backend: %v", err).WithCause(err)
		}
		if !exists {
			return connect.Errorf(connect.CodeNotFound, "backend %s does not exist", backendUuid)
		}
		// uuids are no secret, knowing the one of a backend must not be enough to send traffic to it
		err = s.checkPermission(ctx, helpers.TypeUrl[*backend.Backend](), backendUuid, permission.Role_READER)
		if err != nil {
			return err
		}
	}

	certUuids := virtServ.GetCertificatesUuids()
	for _, certUuid := range certUuids {
		exists, err := s.storeMan.Certificates().Exists(ctx, certUuid)
		if err != nil {
			return connect.Errorf(connect.CodeInternal, "could not find certificate: %v", err).WithCause(err)
		}
		if !exists {
			return connect.Errorf(connect.CodeNotFound, "certificate %s does not exist", certUuid)
		}
		err = s.checkPermission(ctx, helpers.TypeUrl[*cert.Certificate](), certUuid, permission.Role_READER)
		if err != nil {
			return err
		}
	}

	return nil
}

func checkExternalAuths(config *vs.HttpRoutesConfig) error {
	defined := make(map[string]bool)
	for _, policy := range config.GetExternalAuths() {
		if defined[policy.GetName()] {
			return connect.Errorf(connect.CodeInvalidArgument, "external auth %s is defined twice", policy.GetName())
		}
		defined[policy.GetName()] = true
		err := checkUpstreamHeadersOf(policy)
		if err != nil {
			return err
		}
	}

	// an empty name means no policy named, the route inherits its virtual host
	used := make([]string, 0)
	for _, vhost := range config.GetVirtualHosts() {
		used = append(used, vhost.GetExternalAuth())
		for _, rt := range vhost.GetRoutes() {
			used = append(used, rt.GetExternalAuthName())
		}
	}
	for _, name := range used {
		if name != "" && !defined[name] {
			return connect.Errorf(connect.CodeInvalidArgument, "external auth %s is not defined in external_auths", name)
		}
	}
	return nil
}

// checkUpstreamHeadersOf refuses an upstream header the check itself needs, since the inbound copy of every upstream
// header is removed before the check is sent and the check would never see it.
func checkUpstreamHeadersOf(policy *route.ExternalAuth) error {
	for _, upstream := range policy.GetUpstreamHeaders() {
		name := strings.ToLower(upstream)
		isSentToCheck := name == "authorization" || slices.ContainsFunc(policy.GetRequestHeaders(), func(request string) bool {
			return strings.EqualFold(request, name)
		})
		if isSentToCheck {
			return connect.Errorf(connect.CodeInvalidArgument,
				"external auth %s: upstream header %s is also sent to the check, it would be removed from the request before the check sees it",
				policy.GetName(), name)
		}
	}
	return nil
}

func (s *FlockServer) checkPortsOfVips(ctx context.Context, virtServ *vs.VirtualServer) error {
	vips, err := s.storeMan.VIPs().List(ctx, nil, &svccore.ListFilter{
		Key:      helpers.TypeUrl[*vs.VirtualServer](),
		Operator: svccore.ListFilter_EQUAL,
		Value:    virtServ.GetUUID(),
	})
	if err != nil {
		return errors.ToConnectError(err)
	}
	serverUuids := make([]string, 0)
	for _, aVip := range vips {
		serverUuids = append(serverUuids, aVip.GetVirtualServersUuid()...)
	}
	serverByUuid, err := s.virtualServersByUuid(ctx, serverUuids)
	if err != nil {
		return err
	}
	serverByUuid[virtServ.GetUUID()] = virtServ

	for _, aVip := range vips {
		servers := make([]*vs.VirtualServer, 0, len(aVip.GetVirtualServersUuid()))
		for _, virtualServerUuid := range aVip.GetVirtualServersUuid() {
			servers = append(servers, serverByUuid[virtualServerUuid])
		}
		err = checkVipPorts(servers)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *FlockServer) ReadVS(ctx context.Context, req *svccore.UuidRequest) (*svcvs.VSResponse, error) {
	err := s.checkPermission(ctx, helpers.TypeUrl[*vs.VirtualServer](), req.GetUUID(), permission.Role_READER)
	if err != nil {
		return nil, err
	}

	virtserv, err := s.storeMan.VirtualServers().Read(ctx, req.GetUUID())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	return &svcvs.VSResponse{
		Vs: virtserv,
	}, nil
}

func (s *FlockServer) UpdateVS(ctx context.Context, req *svcvs.UpdateVSRequest) (*svcvs.VSResponse, error) {
	err := s.checkPermission(ctx, helpers.TypeUrl[*vs.VirtualServer](), req.GetUUID(), permission.Role_WRITER)
	if err != nil {
		return nil, err
	}

	virtServ := req.GetVs()
	virtServ.UUID = req.GetUUID()

	err = checkNameChangedByOwner[*vs.VirtualServer](ctx, s.storeMan, virtServ, func() error {
		return s.checkPermission(ctx, helpers.TypeUrl[*vs.VirtualServer](), req.GetUUID(), permission.Role_OWNER)
	})
	if err != nil {
		return nil, err
	}

	sig, err := helpers.MessageSignature(virtServ)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "could not create signature: %v", err).WithCause(err)
	}

	err = s.checkVsRequest(ctx, virtServ)
	if err != nil {
		return nil, err
	}

	err = s.checkPortsOfVips(ctx, virtServ)
	if err != nil {
		return nil, err
	}

	storedVs, err := s.storeMan.VirtualServers().Read(ctx, req.GetUUID())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}
	err = s.checkSecretRefs(ctx, storedVs, virtServ)
	if err != nil {
		return nil, err
	}

	err = s.storeMan.VirtualServers().Update(ctx, virtServ.UUID, virtServ)
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	s.spreadPermToMembers(ctx, helpers.TypeUrl[*vs.VirtualServer](), virtServ.UUID)

	observe.EmitSubject(observer.EventType_UPDATE_SUBJECT, &observer.Subject{
		TypeUrl:    helpers.TypeUrlFromMessage(virtServ),
		Identifier: virtServ.UUID,
		Signature:  sig,
	})

	return &svcvs.VSResponse{
		Vs: virtServ,
	}, nil
}

func (s *FlockServer) DeleteVS(ctx context.Context, req *svccore.UuidRequest) (*emptypb.Empty, error) {
	err := s.checkPermission(ctx, helpers.TypeUrl[*vs.VirtualServer](), req.GetUUID(), permission.Role_OWNER)
	if err != nil {
		return nil, err
	}

	founds, err := s.storeMan.VIPs().FindSrcWithForeignResource(ctx, helpers.TypeUrl[*vs.VirtualServer](), req.GetUUID())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}
	if len(founds) > 0 {
		return nil, connect.Errorf(connect.CodeFailedPrecondition,
			"virtual server %s is used by vip(s) %s .", req.GetUUID(), strings.Join(founds, ", "),
		)
	}

	err = s.storeMan.VirtualServers().Delete(ctx, req.GetUUID())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	observe.EmitSubject(observer.EventType_DELETE_SUBJECT, &observer.Subject{
		TypeUrl:    helpers.TypeUrl[*vs.VirtualServer](),
		Identifier: req.GetUUID(),
	})

	return &emptypb.Empty{}, nil
}

func (s *FlockServer) ListVS(ctx context.Context, req *svcvs.ListVSRequest) (*svcvs.ListVSResponse, error) {
	virtServs, err := s.storeMan.VirtualServers().List(ctx, s.makeListOptions(ctx, req), req.GetFilters()...)
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	return &svcvs.ListVSResponse{
		Vs: virtServs,
	}, nil
}
