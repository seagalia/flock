package serves

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/hashicorp/go-multierror"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

// checkVipPorts refuses the virtual servers of one vip that the agents can't serve together. A port holds one virtual
// server, or several https and tls ones, which are told apart by the SNI of each connection.
func checkVipPorts(servers []*vs.VirtualServer) error {
	serversByPort := make(map[string][]*vs.VirtualServer)
	for _, server := range servers {
		portId := fmt.Sprintf("tcp/%d", server.GetPort())
		if server.GetUdpRouteConfig() != nil {
			portId = fmt.Sprintf("udp/%d", server.GetPort())
		}
		serversByPort[portId] = append(serversByPort[portId], server)
	}

	var result error
	for _, portId := range slices.Sorted(maps.Keys(serversByPort)) {
		group := serversByPort[portId]
		if len(group) < 2 {
			continue
		}
		err := checkSharedPort(group)
		if err == nil {
			continue
		}
		names := make([]string, 0, len(group))
		for _, server := range group {
			names = append(names, server.GetName())
		}
		result = multierror.Append(result, connect.Errorf(connect.CodeInvalidArgument,
			"port %s is used by virtual servers %s: %s", portId, strings.Join(names, ", "), err))
	}
	return result
}

func checkSharedPort(group []*vs.VirtualServer) error {
	firstSettings, err := portSettingsOf(group[0])
	if err != nil {
		return err
	}
	ownerOfName := make(map[string]*vs.VirtualServer)
	var catchAllOwner *vs.VirtualServer
	for _, server := range group {
		if !routesOnSni(server) {
			return fmt.Errorf("%s is not routed on SNI, only tls virtual servers and https ones whose virtual hosts all "+
				"have TLS options can share a port", server.GetName())
		}
		settings, err := portSettingsOf(server)
		if err != nil {
			return err
		}
		if settings != firstSettings {
			return fmt.Errorf("%s and %s disagree on the settings of the port, they must have the same "+
				"accept_proxy_protocol, connection limit and connection rate limit", group[0].GetName(), server.GetName())
		}

		// a connection without SNI, or with an unknown one, can only go to one virtual server
		serverNames, hasCatchAll := serverNamesOf(server)
		if hasCatchAll && catchAllOwner != nil {
			return fmt.Errorf("%s and %s both have a catch-all without server names", catchAllOwner.GetName(), server.GetName())
		}
		if hasCatchAll {
			catchAllOwner = server
		}
		for _, serverName := range serverNames {
			// server names are domain names, which ignore case
			serverName = strings.ToLower(serverName)
			owner, taken := ownerOfName[serverName]
			if taken && owner != server {
				return fmt.Errorf("server name %s is routed by %s and %s", serverName, owner.GetName(), server.GetName())
			}
			ownerOfName[serverName] = server
		}
	}
	return nil
}

func routesOnSni(server *vs.VirtualServer) bool {
	if server.GetTlsTcpRoutesConfig() != nil {
		return true
	}
	if server.GetHttpRoutesConfig() == nil {
		return false
	}
	for _, host := range server.GetHttpRoutesConfig().GetVirtualHosts() {
		if len(host.GetTlsOptions()) == 0 {
			return false
		}
	}
	return true
}

// portSettings are the settings of a virtual server that agents apply to the whole port, before the SNI is known.
type portSettings struct {
	acceptProxyProtocol bool
	connLimit           string
	connRateLimit       string
}

func portSettingsOf(server *vs.VirtualServer) (portSettings, error) {
	settings := portSettings{
		acceptProxyProtocol: server.GetHttpRoutesConfig().GetAcceptProxyProtocol() ||
			server.GetTlsTcpRoutesConfig().GetAcceptProxyProtocol(),
	}
	var err error
	if connLimit := server.GetFirstConnLimit(); connLimit != nil {
		settings.connLimit, err = helpers.MessageSignature(connLimit)
		if err != nil {
			return settings, err
		}
	}
	if connRateLimit := server.GetFirstConnRateLimit(); connRateLimit != nil {
		settings.connRateLimit, err = helpers.MessageSignature(connRateLimit)
		if err != nil {
			return settings, err
		}
	}
	return settings, nil
}

func serverNamesOf(server *vs.VirtualServer) ([]string, bool) {
	var serverNames []string
	hasCatchAll := false
	addChain := func(chainNames []string) {
		serverNames = append(serverNames, chainNames...)
		hasCatchAll = hasCatchAll || len(chainNames) == 0
	}
	for _, rte := range server.GetTlsTcpRoutesConfig().GetRoutes() {
		if passthrough := rte.GetPassthrough(); passthrough != nil {
			addChain(passthrough.GetServerNames())
			continue
		}
		addChain(rte.GetTls().GetServerNames())
	}
	for _, host := range server.GetHttpRoutesConfig().GetVirtualHosts() {
		for _, tlsOpt := range host.GetTlsOptions() {
			addChain(tlsOpt.GetServerNames())
		}
	}
	return serverNames, hasCatchAll
}
