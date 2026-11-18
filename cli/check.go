package cli

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"fmt"
	"net"
	"runtime"
	"slices"
	"strings"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	agentapi "github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"golang.org/x/sync/errgroup"
)

type Check struct {
	IpPort NameUuid `arg:"" name:"ip-port" help:"ip or/and port for checking vip/virtual server in format 127.0.0.1:80 or [::]:80 where :port can be omitted"`

	client    *clients.Client
	ipGiven   string
	portGiven string
}

type CheckResult struct {
	Instances  []*instance.Instance
	vip        *vip.VIP
	vss        []*vs.VirtualServer
	certs      []*cert.Certificate
	backends   []*backend.Backend
	vipBackend map[string][]string
}

func (c *Check) SetClient(client *clients.Client) {
	c.client = client
}

func (c *Check) Run(ctx context.Context) error {
	if c.IpPort.Identifier() == "" {
		return fmt.Errorf("ip with optional port is required")
	}
	ipGiven, portGiven, err := net.SplitHostPort(c.IpPort.Identifier())
	if err != nil {
		ipGiven = c.IpPort.Identifier()
		portGiven = ""
	}
	// SplitHostPort removes ipv6 brackets only when a port is given.
	c.ipGiven = strings.Trim(ipGiven, "[]")
	c.portGiven = portGiven

	spinner := msg.GetSpinner()
	spinner.Message("Getting all informations")
	spinner.Start()
	defer spinner.Stop()
	checkResult, err := c.getCheckResult(ctx)
	if err != nil {
		return err
	}
	if len(checkResult.Instances) == 0 {
		msg.Info("This vip or/and virtual server is not attached to any instance, this might be the problem.")
		return nil
	}
	spinner.Stop()
	for _, inst := range checkResult.Instances {
		err = c.PrintResource(ctx, inst, checkResult)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *Check) PrintResource(ctx context.Context, inst *instance.Instance, checkResult *CheckResult) error {
	listAgents, err := c.client.Lookup().ListAgent(ctx, &svclookup.ListAgentRequest{
		InstanceUuid: inst.GetUUID(),
	})
	if err != nil {
		return err
	}
	agents := make([]string, 0)
	provisioners := make([]string, 0)
	for _, agent := range listAgents.Agents {
		if agent.GetMode() == agentapi.Agent_PROVISIONER {
			provisioners = append(provisioners, agent.UUID)
			continue
		}
		agents = append(agents, agent.UUID)
	}
	if c.portGiven != "" && len(checkResult.vss) == 0 {
		msg.Warningf("No virtual server with port %s found for this vip.\n", msg.Red(c.portGiven).String())
		return nil
	}

	msg.Infof("Found %s on instance %s, check result:\n",
		msg.Green(checkResult.vip.GetIp()),
		msg.Cyan(inst.GetName()),
	)

	msg.Info("Vip(s) check configuration result: \n")

	err = c.printResultForType(ctx, inst, checkResult, agents, provisioners, helpers.TypeUrl[*vip.VIP](), []string{checkResult.vip.GetUUID()})
	if err != nil {
		return err
	}

	if len(checkResult.vss) == 0 {
		msg.Warning("No virtual server found for this vip.\n")
		return nil
	}
	msg.Println("")
	msg.Info("Virtual server(s) check configuration result: \n")
	uuids := make([]string, 0)
	for _, vsInfo := range checkResult.vss {
		uuids = append(uuids, vsInfo.GetUUID())
	}
	err = c.printResultForType(ctx, inst, checkResult, agents, provisioners, helpers.TypeUrl[*vs.VirtualServer](), uuids)
	if err != nil {
		return err
	}

	if len(checkResult.certs) > 0 {
		msg.Println("")
		msg.Info("Certificate(s) check configuration result: \n")
		uuids := make([]string, 0)
		for _, res := range checkResult.certs {
			uuids = append(uuids, res.GetUUID())
		}
		err = c.printResultForType(ctx, inst, checkResult, agents, provisioners, helpers.TypeUrl[*cert.Certificate](), uuids)
		if err != nil {
			return err
		}
	}
	if len(checkResult.backends) == 0 {
		return nil
	}
	msg.Println("")
	msg.Info("Backend(s) check configuration result: \n")
	uuids = make([]string, 0)
	for _, res := range checkResult.backends {
		uuids = append(uuids, res.GetUUID())
	}
	err = c.printResultForType(ctx, inst, checkResult, agents, provisioners, helpers.TypeUrl[*backend.Backend](), uuids)
	if err != nil {
		return err
	}
	msg.Println("")
	msg.Info("Endpoints status result: \n")
	err = c.printResultForType(ctx, inst, checkResult, agents, provisioners, helpers.TypeUrl[*backend.BackendEndpointStats](), uuids)
	if err != nil {
		return err
	}
	return nil
}

func (c *Check) printResultForType(ctx context.Context, inst *instance.Instance, checkResult *CheckResult, agents, provisioners []string, typeUrl string, uuids []string) error {

	agents = append(slices.Clone(agents), provisionersReporting(ctx, c.client, inst.GetUUID(), provisioners, typeUrl)...)
	// one slot per agent, so the goroutines share nothing and the errors come out in the order of the agents
	agentResponses := make([][]*svclookup.StatusResponse, len(agents))
	agentErrors := make([]error, len(agents))
	var g errgroup.Group
	g.SetLimit(runtime.GOMAXPROCS(0))
	for i, agent := range agents {
		g.Go(func() error {
			statusResponses := make([]*svclookup.StatusResponse, 0)
			for _, resUuid := range uuids {
				statusResponse, err := c.client.Lookup().GetStatus(ctx, &svclookup.GetStatusRequest{
					InstanceUuid: inst.GetUUID(),
					AgentUuid:    agent,
					TypeUrl:      typeUrl,
					Identifier:   resUuid,
				})
				if err != nil {
					agentErrors[i] = fmt.Errorf("on agent %s: %w", agent, err)
					return nil
				}
				statusResponses = append(statusResponses, statusResponse)
			}
			agentResponses[i] = statusResponses
			return nil
		})
	}
	_ = g.Wait()
	// every call failed with the cancellation, listing it per agent would bury the interruption
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// printed before the tables, the user reads them as the reason a row is missing
	for _, err := range agentErrors {
		if err != nil {
			msg.Error(err.Error())
		}
	}
	responses := make([]*svclookup.StatusResponse, 0)
	for _, statusResponses := range agentResponses {
		responses = append(responses, statusResponses...)
	}

	slices.SortFunc(responses, func(a, b *svclookup.StatusResponse) int {
		return cmp.Compare(a.AgentUuid, b.AgentUuid)
	})
	if typeUrl == helpers.TypeUrl[*backend.BackendEndpointStats]() {
		return c.printEndpoints(checkResult, responses)
	}

	return c.printAnyResource(responses)
}

func (c *Check) printEndpoints(checkResult *CheckResult, responses []*svclookup.StatusResponse) error {
	buf := &bytes.Buffer{}
	table := MakeTableWriter([]string{"Agent UUID", "Virtual Server/IP:PORT", "State", "Endpoint", "Endpoint State", "Endpoint Error"}, buf)
	table.Options(tableRowLines(), tableMergedColumns(0, 1, 2))
	autoColorSess := NewAutoColor()
	currentAgent := ""
	for _, statusResponse := range responses {
		endpointStats := &backend.BackendEndpointStats{}
		err := statusResponse.GetPreviousResource().UnmarshalTo(endpointStats)
		if err != nil {
			return err
		}
		healthy := msg.Green("Alive").String()
		if !endpointStats.GetHealthy() {
			healthy = msg.Red("Dead").String()
		}
		for _, endpointStat := range endpointStats.GetEndpointStats() {
			if !endpointStat.GetHealthy() && endpointStats.GetHealthy() {
				healthy = msg.Yellow("Warning").String()
			}
		}
		if currentAgent != statusResponse.AgentUuid && len(endpointStats.GetEndpointStats()) > 0 {
			if currentAgent != "" {
				if err := table.Append([]string{"", "", "", "", "", ""}); err != nil {
					return err
				}
			}
			currentAgent = statusResponse.AgentUuid
		}
		for _, endpointStat := range endpointStats.GetEndpointStats() {
			healthyEndpoint := msg.Green("Healthy").String()
			if !endpointStat.GetHealthy() {
				healthyEndpoint = msg.Red("Unhealthy").String()
			}

			vsExp, ok := checkResult.vipBackend[endpointStats.GetBackendUuid()]
			if !ok {
				vsExp = []string{"Backend UUID: " + endpointStats.GetBackendUuid()}
			}
			cols := []string{
				msg.Colorize(statusResponse.AgentUuid, autoColorSess.GetColor(statusResponse.AgentUuid)).String(),
				strings.Join(vsExp, "\n"),
				healthy,
				endpointStat.GetIpAddr().Format(),
				healthyEndpoint,
				endpointStat.GetLastError().GetValue(),
			}
			if err := table.Append(cols); err != nil {
				return err
			}
		}
	}

	if err := table.Render(); err != nil {
		return err
	}

	scanner := bufio.NewScanner(buf)
	prevIsNewLine := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "+-") {
			prevIsNewLine = true
			msg.Println(line)
			continue
		}
		if !prevIsNewLine {
			msg.Println(line)
			continue
		}
		prevIsNewLine = false
		if pipeEmptyLine.MatchString(line) {
			line = strings.Replace(line, "|", " ", -1)
		}
		msg.Println(line)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func (c *Check) printAnyResource(responses []*svclookup.StatusResponse) error {
	table := MakeTableWriter([]string{"Agent UUID", "Resource Name", "State", "Diff Config Previous/Errored", "Error"})
	table.Options(tableWrap(false), tableRowLines(), tableMergedColumns(0))
	var err error
	autoColorSess := NewAutoColor()
	for _, statusResponse := range responses {
		state := resourceStateLabel(statusResponse.GetState())
		diff := ""
		if statusResponse.ErroredResource == nil {
			diff = msg.Blue("No difference.").String()
		}

		if statusResponse.ErroredResource != nil {
			diff, err = ProtoDiffContent(statusResponse.PreviousResource, statusResponse.ErroredResource)
			if err != nil {
				return err
			}
		}

		if statusResponse.Error == "" {
			diff = msg.Green("There is no error").String()
		}

		if err := table.Append([]string{
			msg.Colorize(statusResponse.AgentUuid, autoColorSess.GetColor(statusResponse.AgentUuid)).String(),
			msg.Cyan(NameFromAny(statusResponse.PreviousResource)).String(),
			state,
			diff,
			errorAndNotes(statusResponse),
		}); err != nil {
			return err
		}
	}
	return table.Render()
}

func (c *Check) getInstances(ctx context.Context, vipUuid string) ([]*instance.Instance, error) {
	var offset = 0
	var limit = 100
	var instancesList []*instance.Instance

	for {
		response, err := c.client.Instance().ListInstance(ctx, &svcinstance.ListInstanceRequest{
			Limit:  int32(limit),
			Offset: int32(offset),
			Filters: []*svccore.ListFilter{
				{
					Key:   helpers.TypeUrl[*vip.VIP](),
					Value: vipUuid,
				},
			},
		})
		if err != nil {
			return nil, err
		}
		instancesList = append(instancesList, response.GetInstances()...)
		if len(response.GetInstances()) == 0 {
			break
		}
		offset += limit
	}
	return instancesList, nil
}

func (c *Check) getCertificates(ctx context.Context, typeRes, uuid string) ([]*cert.Certificate, error) {
	var offset = 0
	var limit = 100
	var resources []*cert.Certificate

	for {
		response, err := c.client.Certificate().ListCertificate(ctx, &svccert.ListCertificateRequest{
			Limit:  int32(limit),
			Offset: int32(offset),
			Filters: []*svccore.ListFilter{
				{
					Key:   typeRes,
					Value: uuid,
				},
			},
		})
		if err != nil {
			return nil, err
		}
		resources = append(resources, response.GetCertificates()...)
		if len(response.GetCertificates()) == 0 {
			break
		}
		offset += limit
	}
	return resources, nil
}

func (c *Check) getBackends(ctx context.Context, vsUuid string) ([]*backend.Backend, error) {
	var offset = 0
	var limit = 100
	var resources []*backend.Backend

	for {
		response, err := c.client.Backend().ListBackend(ctx, &svcbackend.ListBackendRequest{
			Limit:  int32(limit),
			Offset: int32(offset),
			Filters: []*svccore.ListFilter{
				{
					Key:   helpers.TypeUrl[*vs.VirtualServer](),
					Value: vsUuid,
				},
			},
		})
		if err != nil {
			return nil, err
		}
		resources = append(resources, response.GetBackends()...)
		if len(response.GetBackends()) == 0 {
			break
		}
		offset += limit
	}
	return resources, nil
}

func (c *Check) getCheckResult(ctx context.Context) (*CheckResult, error) {
	vipBackend := make(map[string][]string)
	checkResult := &CheckResult{}
	var offset, limit int32 = 0, 100
	var vipsList []*vip.VIP

	for {
		response, err := c.client.Vip().ListVIP(ctx, &svcvip.ListVIPRequest{
			Limit:  limit,
			Offset: offset,
		})
		if err != nil {
			return nil, err
		}
		vipsList = append(vipsList, response.GetVip()...)
		if len(response.GetVip()) == 0 {
			break
		}
		offset += limit
	}
	for _, vipInfo := range vipsList {
		var offset = 0
		var limit = 100
		var vsList []*vs.VirtualServer

		filters := []*svccore.ListFilter{
			{
				Key:   helpers.TypeUrl[*vip.VIP](),
				Value: vipInfo.GetUUID(),
			},
		}

		for {
			response, err := c.client.VirtualServer().ListVS(ctx, &svcvs.ListVSRequest{
				Limit:   int32(limit),
				Offset:  int32(offset),
				Filters: filters,
			})
			if err != nil {
				return nil, err
			}
			vsList = append(vsList, response.GetVs()...)
			if len(response.GetVs()) == 0 {
				break
			}
			offset += limit
		}
		allVs := make([]*vs.VirtualServer, 0)
		ipStr := vipInfo.GetIp()
		if strings.Contains(vipInfo.GetIp(), ":") {
			ipStr = "[" + vipInfo.GetIp() + "]"
		}
		if c.ipGiven != vipInfo.GetIp() {
			continue
		}
		for _, vsInfo := range vsList {
			if !strings.Contains(fmt.Sprintf("%s:%d", ipStr, vsInfo.GetPort()), c.IpPort.Identifier()) {
				continue
			}
			for _, backendUuid := range vsInfo.GetBackendUuids() {
				listVsExp, ok := vipBackend[backendUuid]
				if !ok {
					listVsExp = make([]string, 0)
				}
				listVsExp = append(listVsExp, fmt.Sprintf("%s/%s:%d",
					msg.Cyan(vsInfo.GetName()).String(),
					ipStr,
					vsInfo.GetPort(),
				))
				vipBackend[backendUuid] = listVsExp
			}
			allVs = append(allVs, vsInfo)
		}
		checkResult.vip = vipInfo
		checkResult.vss = allVs
		checkResult.vipBackend = vipBackend
		break
	}
	if checkResult.vip == nil {
		return nil, fmt.Errorf("no vip found for %s", c.IpPort.Identifier())
	}
	instances, err := c.getInstances(ctx, checkResult.vip.GetUUID())
	if err != nil {
		return nil, err
	}
	checkResult.Instances = instances
	checkResult.certs = make([]*cert.Certificate, 0)
	checkResult.backends = make([]*backend.Backend, 0)

	certsMap := make(map[string]*cert.Certificate)
	backendsMap := make(map[string]*backend.Backend)

	for _, vsInfo := range checkResult.vss {
		backends, err := c.getBackends(ctx, vsInfo.GetUUID())
		if err != nil {
			return nil, err
		}
		for _, backendInfo := range backends {
			backendsMap[backendInfo.GetUUID()] = backendInfo
		}
		certs, err := c.getCertificates(ctx, helpers.TypeUrl[*vs.VirtualServer](), vsInfo.GetUUID())
		if err != nil {
			return nil, err
		}
		for _, certInfo := range certs {
			certsMap[certInfo.GetUUID()] = certInfo
		}
	}
	for _, certInfo := range certsMap {
		checkResult.certs = append(checkResult.certs, certInfo)
	}
	for _, backendInfo := range backendsMap {
		checkResult.backends = append(checkResult.backends, backendInfo)
	}

	return checkResult, nil
}
