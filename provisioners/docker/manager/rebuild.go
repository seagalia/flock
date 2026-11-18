package manager

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/seagalia/flock/provisioners/docker/models"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
)

// joinedVsKey records on a backend the virtual server its container joined by label, a virtual server of a user the
// provisioner doesn't own: once the container is gone or joins another one, it tells where its routes must be removed
// from.
const joinedVsKey = "flock.docker.virtual_server"

// rebuildRun collects what one rebuild decided, for the steps after the services
type rebuildRun struct {
	// joins are the virtual hosts to put in each http virtual server, by uuid or name
	joins map[string][]*route.VirtualHost
	// tlsJoins are the routes to put in each TLS over tcp virtual server, by uuid or name
	tlsJoins map[string][]tlsJoin
	// attachVsUuids are the owned virtual servers the vip must hold
	attachVsUuids []string
	// deleteVss are the owned virtual servers left with nothing to serve
	deleteVss []*vs.VirtualServer
	// backendUuids are the backends written by this rebuild, by service name
	backendUuids map[string]string
	// authPolicies are the external auth policies the running containers provide, by policy name
	authPolicies map[string]*route.ExternalAuth
}

// rebuild brings the eds and the controller in line with the running containers. A service failing is logged and left
// as it is on the controller, the others go on.
func (m *Manager) rebuild(ctx context.Context) {
	listed, err := m.docker.ContainerList(ctx, client.ContainerListOptions{
		Filters: make(client.Filters).Add("status", "running"),
	})
	if err != nil {
		m.entry.Error().Err(err).Msg("list containers failed")
		return
	}
	// before the controller writes, so a backend created below finds its endpoints at once
	err = m.edsServer.Replace(listed.Items)
	if err != nil {
		m.entry.Warn().Err(err).Msg("the eds keeps its previous endpoints")
	}

	stored, err := m.ownedResources(ctx)
	if err != nil {
		m.entry.Error().Err(err).Msg("list the resources of the provisioner failed")
		return
	}
	bases := serviceBases(listed.Items)
	// read before applyService rewrites the backends with the virtual servers their labels name now
	previouslyJoined := joinedByLabel(stored)
	run := &rebuildRun{
		joins:        map[string][]*route.VirtualHost{},
		tlsJoins:     map[string][]tlsJoin{},
		backendUuids: map[string]string{},
		authPolicies: map[string]*route.ExternalAuth{},
	}
	for _, name := range slices.Sorted(maps.Keys(bases)) {
		err := m.applyService(ctx, bases[name], stored, run)
		if err != nil {
			m.entry.Error().Err(err).Str("service", name).Msg("provision service failed")
		}
	}
	m.collectAuthPolicies(bases, run)
	m.dropRoutesOfUnknownPolicies(run)

	gone := goneServices(stored, bases)
	m.applyJoinedVirtualServers(ctx, stored, previouslyJoined, run)
	run.deleteVss = append(run.deleteVss, m.unusedTcpUdpVirtualServers(stored, bases)...)

	err = m.attachToVip(ctx, run)
	if err != nil {
		// the virtual servers to delete may still be on the vip, the controller would refuse their delete
		m.entry.Error().Err(err).Msg("attach the virtual servers to the vip failed")
		return
	}
	for _, unused := range run.deleteVss {
		err := m.deleteVs(ctx, unused)
		if err != nil {
			m.entry.Error().Err(err).Msg("delete virtual server failed")
		}
	}
	for _, service := range gone {
		err := m.deleteBackend(ctx, stored.backends[models.NamePrefix+service])
		if err != nil {
			m.entry.Error().Err(err).Str("service", service).Msg("delete backend failed")
		}
	}
}

// serviceBases keeps one container per service, the one of the smallest id, so the choice holds from one list to the
// next. Its replicas share their labels.
func serviceBases(containers []container.Summary) map[string]*models.Base {
	bases := map[string]*models.Base{}
	ids := map[string]string{}
	for _, summary := range containers {
		// the eds server already reported the containers in error
		base, err := models.Decode(summary)
		if err != nil || base == nil {
			continue
		}
		knownId, isKnown := ids[base.Name]
		if isKnown && knownId < summary.ID {
			continue
		}
		bases[base.Name] = base
		ids[base.Name] = summary.ID
	}
	return bases
}

func joinedByLabel(stored *owned) []string {
	var refs []string
	for _, backend := range stored.backends {
		ref, hasJoined := backend.GetMetadata()[joinedVsKey]
		if hasJoined {
			refs = append(refs, ref)
		}
	}
	return refs
}

// goneServices are the services the provisioner made a backend for and no container runs any more
func goneServices(stored *owned, bases map[string]*models.Base) []string {
	var gone []string
	for name := range stored.backends {
		service, isDocker := strings.CutPrefix(name, models.NamePrefix)
		_, isRunning := bases[service]
		if isDocker && !isRunning {
			gone = append(gone, service)
		}
	}
	slices.Sort(gone)
	return gone
}

// collectAuthPolicies keeps one policy per name, the one of the first service in sorted order, so the choice holds from
// one rebuild to the next. A provider whose backend failed provides nothing.
func (m *Manager) collectAuthPolicies(bases map[string]*models.Base, run *rebuildRun) {
	providers := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(bases)) {
		backendUuid, hasBackend := run.backendUuids[name]
		if !hasBackend {
			continue
		}
		policy, err := m.converter.ExternalAuth(bases[name], backendUuid)
		if err != nil {
			m.entry.Error().Err(err).Str("service", name).Msg("provide external auth failed")
			continue
		}
		if policy == nil {
			continue
		}
		provider, isTaken := providers[policy.GetName()]
		if isTaken {
			m.entry.Error().Str("service", name).Str("policy", policy.GetName()).
				Msgf("external auth %s is already provided by %s", policy.GetName(), provider)
			continue
		}
		providers[policy.GetName()] = name
		run.authPolicies[policy.GetName()] = policy
	}
}

// dropRoutesOfUnknownPolicies leaves out the routes naming a policy no running container provides: the service is not
// served at all rather than served unchecked, and the controller would refuse the whole virtual server for it.
func (m *Manager) dropRoutesOfUnknownPolicies(run *rebuildRun) {
	for ref, virtualHosts := range run.joins {
		run.joins[ref] = slices.DeleteFunc(virtualHosts, func(virtualHost *route.VirtualHost) bool {
			for _, r := range virtualHost.GetRoutes() {
				policy := r.GetExternalAuthName()
				_, isProvided := run.authPolicies[policy]
				if policy != "" && !isProvided {
					m.entry.Error().Str("service", r.GetName()).Str("policy", policy).
						Msgf("no running container provides the external auth %s", policy)
					return true
				}
			}
			return false
		})
	}
}

func (m *Manager) applyService(ctx context.Context, base *models.Base, stored *owned, run *rebuildRun) error {
	backend, err := m.converter.Backend(base)
	if err != nil {
		return err
	}
	isJoiningByLabel := base.VirtualServer != nil && !base.HasOwnVirtualServer()
	if isJoiningByLabel {
		backend.Metadata[joinedVsKey] = *base.VirtualServer
	}
	backendUuid, err := m.writeBackend(ctx, backend, stored.backends[backend.GetName()])
	if err != nil {
		return err
	}
	run.backendUuids[base.Name] = backendUuid

	switch {
	case base.IsHttp():
		return m.joinHttp(ctx, base, backendUuid, stored, run)
	case base.IsTlsTcp():
		return m.joinTlsTcp(ctx, base, backendUuid, stored, run)
	default:
		return m.writeOwnVirtualServer(ctx, base, backendUuid, stored, run)
	}
}

func (m *Manager) joinHttp(
	ctx context.Context, base *models.Base, backendUuid string, stored *owned, run *rebuildRun,
) error {
	certUuid, err := m.certificateUuid(ctx, base, stored)
	if err != nil {
		return err
	}
	virtualHost, err := m.converter.VirtualHost(base, backendUuid, certUuid)
	if err != nil {
		return err
	}
	ref := m.converter.JoinedVirtualServerRef(base)
	run.joins[ref] = append(run.joins[ref], virtualHost)
	redirect, err := m.converter.RedirectVirtualHost(base)
	if err != nil {
		return err
	}
	if redirect != nil {
		run.joins[models.DefaultHttpVirtualServer] = append(run.joins[models.DefaultHttpVirtualServer], redirect)
	}
	return nil
}

// tlsJoin keeps the base of the container with its route, the first one of a port builds its virtual server
type tlsJoin struct {
	base  *models.Base
	route *vs.TlsTcpRouteConfig
}

func (m *Manager) joinTlsTcp(
	ctx context.Context, base *models.Base, backendUuid string, stored *owned, run *rebuildRun,
) error {
	certUuid, err := m.certificateUuid(ctx, base, stored)
	if err != nil {
		return err
	}
	tlsRoute, err := m.converter.TlsTcpRoute(base, backendUuid, certUuid)
	if err != nil {
		return err
	}
	ref, err := m.converter.TlsTcpVirtualServerRef(base)
	if err != nil {
		return err
	}
	run.tlsJoins[ref] = append(run.tlsJoins[ref], tlsJoin{base: base, route: tlsRoute})
	return nil
}

func (m *Manager) writeOwnVirtualServer(
	ctx context.Context, base *models.Base, backendUuid string, stored *owned, run *rebuildRun,
) error {
	desired, err := m.converter.VirtualServer(base, backendUuid)
	if err != nil {
		return err
	}
	vsUuid, err := m.writeVs(ctx, desired, stored.vss[desired.GetName()])
	if err != nil {
		return err
	}
	run.attachVsUuids = append(run.attachVsUuids, vsUuid)
	return nil
}

func (m *Manager) certificateUuid(ctx context.Context, base *models.Base, stored *owned) (string, error) {
	desired, err := m.converter.Certificate(base)
	if err != nil {
		return "", err
	}
	if desired != nil {
		return m.writeCertificate(ctx, desired, stored.certs[desired.GetName()])
	}
	givenCert := base.GivenCert()
	if givenCert == "" {
		return "", nil
	}
	return m.findCertUuid(ctx, givenCert)
}

func (m *Manager) applyJoinedVirtualServers(
	ctx context.Context, stored *owned, previouslyJoined []string, run *rebuildRun,
) {
	refs := map[string]bool{}
	for ref := range run.joins {
		refs[ref] = true
	}
	for ref := range run.tlsJoins {
		refs[ref] = true
	}
	for name, owned := range stored.vss {
		isJoinable := owned.GetHttpRoutesConfig() != nil || owned.GetTlsTcpRoutesConfig() != nil
		if isJoinable {
			refs[name] = true
		}
	}
	for _, ref := range previouslyJoined {
		refs[ref] = true
	}

	for _, ref := range slices.Sorted(maps.Keys(refs)) {
		err := m.applyJoinedVirtualServer(ctx, ref, stored, run)
		if err != nil {
			m.entry.Error().Err(err).Str("virtual_server", ref).Msg("provision virtual server failed")
		}
	}
}

// applyJoinedVirtualServer removes the routes of every service the provisioner manages before adding back the ones
// the labels ask for, so a route whose domains, path or virtual server changed leaves no copy behind.
func (m *Manager) applyJoinedVirtualServer(ctx context.Context, ref string, stored *owned, run *rebuildRun) error {
	// read right before the write: a virtual server of a user changes on the controller side
	current, err := m.findVs(ctx, ref)
	if err != nil {
		return err
	}
	if current == nil {
		return m.createJoinedVirtualServer(ctx, ref, run)
	}

	updated := current
	for name, backend := range stored.backends {
		service := strings.TrimPrefix(name, models.NamePrefix)
		updated = models.RemoveRoutes(updated, service)
		updated = models.RemoveTlsTcpRoutes(updated, backend.GetUUID())
		updated = models.RemoveExternalAuths(updated, backend.GetUUID())
	}
	updated, err = joinAll(updated, run.joins[ref], run.tlsJoins[ref], run.authPolicies)
	if err != nil {
		return err
	}
	isOwned := stored.vss[current.GetName()].GetUUID() == current.GetUUID()
	isEmpty := hasNoRoute(updated)
	if isEmpty && isOwned {
		run.deleteVss = append(run.deleteVss, current)
		return nil
	}
	if isEmpty {
		return fmt.Errorf("removing the routes of the docker services would leave virtual server %s empty", ref)
	}
	vsUuid, err := m.writeVs(ctx, updated, current)
	if err != nil {
		return err
	}
	if isOwned {
		run.attachVsUuids = append(run.attachVsUuids, vsUuid)
	}
	return nil
}

// createJoinedVirtualServer creates a default virtual server around its first route, when some container joins it
func (m *Manager) createJoinedVirtualServer(ctx context.Context, ref string, run *rebuildRun) error {
	var created *vs.VirtualServer
	var err error
	virtualHosts := run.joins[ref]
	tlsJoins := run.tlsJoins[ref]
	switch {
	case len(virtualHosts) > 0:
		created, err = m.converter.DefaultVirtualServer(ref, virtualHosts[0])
		virtualHosts = virtualHosts[1:]
	case len(tlsJoins) > 0:
		created, err = m.converter.DefaultTlsTcpVirtualServer(tlsJoins[0].base, tlsJoins[0].route)
		tlsJoins = tlsJoins[1:]
	default:
		return nil
	}
	if err != nil {
		return err
	}
	created, err = joinAll(created, virtualHosts, tlsJoins, run.authPolicies)
	if err != nil {
		return err
	}
	vsUuid, err := m.writeVs(ctx, created, nil)
	if err != nil {
		return err
	}
	run.attachVsUuids = append(run.attachVsUuids, vsUuid)
	return nil
}

// joinAll fails on a route of the wrong kind: an http container naming a TLS virtual server, or the reverse. Each
// policy a joined route names comes along, dropRoutesOfUnknownPolicies already left out the routes naming none.
func joinAll(
	virtualServer *vs.VirtualServer,
	virtualHosts []*route.VirtualHost,
	tlsJoins []tlsJoin,
	authPolicies map[string]*route.ExternalAuth,
) (*vs.VirtualServer, error) {
	joined := virtualServer
	var err error
	namedPolicies := map[string]bool{}
	for _, virtualHost := range virtualHosts {
		joined, err = models.JoinVirtualHost(joined, virtualHost)
		if err != nil {
			return nil, err
		}
		for _, r := range virtualHost.GetRoutes() {
			if r.GetExternalAuthName() != "" {
				namedPolicies[r.GetExternalAuthName()] = true
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(namedPolicies)) {
		joined, err = models.JoinExternalAuth(joined, authPolicies[name])
		if err != nil {
			return nil, err
		}
	}
	for _, join := range tlsJoins {
		joined, err = models.JoinTlsTcpRoute(joined, join.route)
		if err != nil {
			return nil, err
		}
	}
	return joined, nil
}

func hasNoRoute(virtualServer *vs.VirtualServer) bool {
	switch {
	case virtualServer.GetHttpRoutesConfig() != nil:
		return len(virtualServer.GetHttpRoutesConfig().GetVirtualHosts()) == 0
	case virtualServer.GetTlsTcpRoutesConfig() != nil:
		return len(virtualServer.GetTlsTcpRoutesConfig().GetRoutes()) == 0
	default:
		return false
	}
}

// unusedTcpUdpVirtualServers are the owned tcp and udp virtual servers no running service asks for. The names asked for
// are computed apart from the writes, so a service failing to write keeps its virtual server.
func (m *Manager) unusedTcpUdpVirtualServers(stored *owned, bases map[string]*models.Base) []*vs.VirtualServer {
	asked := map[string]bool{}
	for _, base := range bases {
		if !base.HasOwnVirtualServer() {
			continue
		}
		desired, err := m.converter.VirtualServer(base, "")
		if err == nil {
			asked[desired.GetName()] = true
		}
	}
	var unused []*vs.VirtualServer
	for name, owned := range stored.vss {
		isTcpUdp := owned.GetTcpRouteConfig() != nil || owned.GetUdpRouteConfig() != nil
		if isTcpUdp && !asked[name] {
			unused = append(unused, owned)
		}
	}
	return unused
}

// attachToVip puts the owned virtual servers on the vip and takes off the ones to delete. The vip is read right before
// and sent whole, since an update replaces it.
func (m *Manager) attachToVip(ctx context.Context, run *rebuildRun) error {
	current, err := m.findVip(ctx, m.cnf.Docker.Vip)
	if err != nil {
		return err
	}
	var deletedUuids []string
	for _, unused := range run.deleteVss {
		deletedUuids = append(deletedUuids, unused.GetUUID())
	}
	attached := slices.DeleteFunc(slices.Clone(current.GetVirtualServersUuid()), func(vsUuid string) bool {
		return slices.Contains(deletedUuids, vsUuid)
	})
	for _, vsUuid := range run.attachVsUuids {
		if !slices.Contains(attached, vsUuid) {
			attached = append(attached, vsUuid)
		}
	}
	if slices.Equal(attached, current.GetVirtualServersUuid()) {
		return nil
	}
	current.VirtualServersUuid = attached
	return m.updateVip(ctx, current)
}
