package cli

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"fmt"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/seagalia/flock/cli/common"
	"github.com/seagalia/flock/cli/tui"
	"github.com/seagalia/flock/cli/tui/status"
	"github.com/seagalia/flock/cli/tui/tuicommon"
	agentapi "github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"golang.org/x/sync/errgroup"

	msg "github.com/ArthurHlt/messages"
	"github.com/mitchellh/go-wordwrap"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

const endpointsRefreshInterval = 10 * time.Second

var pipeEmptyLine = regexp.MustCompile(`\s+(\|\s*){4}\|\s\s+`)

type Status struct {
	Named InstanceNamed `arg:"" name:"instance" help:"instance name or uuid or * for all" optional:"" predictor:"instance"`

	Json bool `short:"j" name:"json" help:"Format in json instead of human table readable."`

	Agent      string   `short:"a" name:"agent" help:"UUID of the agent you would like to get status from"`
	Type       string   `short:"r" name:"resource-type" help:"Type of the resource you would like to get status" enum:",certificate,backend,instance,vip,vs,endpoints" default:""`
	Identifier NameUuid `short:"i" name:"identifier" placeholder:"NAME|UUID" help:"Name or uuid of the resource you would like to get status. If not set it list all resources with type given." predictor:"by-resource-type"`

	NoInteract bool `short:"n" name:"no-interact" help:"Do not interact with the user, just print the result."`
	client     *clients.Client

	// provisioners holds the uuids of the provisioners met while listing agents
	provisioners map[string]bool
}

// The status-<type> commands are status with the resource type already set.
type StatusCertificates struct{ Status }

func (c *StatusCertificates) AfterApply() error {
	c.Type = "certificate"
	return nil
}

type StatusBackends struct{ Status }

func (c *StatusBackends) AfterApply() error {
	c.Type = "backend"
	return nil
}

type StatusVips struct{ Status }

func (c *StatusVips) AfterApply() error {
	c.Type = "vip"
	return nil
}

type StatusVs struct{ Status }

func (c *StatusVs) AfterApply() error {
	c.Type = "vs"
	return nil
}

type StatusEndpoints struct{ Status }

func (c *StatusEndpoints) AfterApply() error {
	c.Type = "endpoints"
	return nil
}

type statusRequest struct {
	uuid      string
	agentUuid string
	typeUrl   string
	resUuid   string
}

func (c *Status) SetClient(client *clients.Client) {
	c.client = client
}

func (c *Status) Run(ctx context.Context) error {
	if c.Named.Identifier() == "" {
		c.Named.identifier = "*"
	}
	if c.Type == "" {
		return fmt.Errorf("the required flag `-r, --resource-type' was not specified")
	}

	if c.Named.Identifier() != "*" {
		inst, err := FindInstance(ctx, c.client, &c.Named)
		if err != nil {
			return err
		}
		if c.isEndpointInteract() {
			return c.executeEndpointInteract(ctx, []*instance.Instance{inst})
		}
		return c.executeByInstance(ctx, inst)
	}

	var offset = 0
	var limit = 100
	var instancesList []*instance.Instance

	for {
		response, err := c.client.Instance().ListInstance(ctx, &svcinstance.ListInstanceRequest{
			Limit:  int32(limit),
			Offset: int32(offset),
		})
		if err != nil {
			return err
		}
		instancesList = append(instancesList, response.GetInstances()...)
		if len(response.GetInstances()) == 0 {
			break
		}
		offset += limit
	}
	if c.isEndpointInteract() {
		return c.executeEndpointInteract(ctx, instancesList)
	}
	for _, inst := range instancesList {
		msg.Infof("Status in Instance %s\n", msg.Cyan(inst.GetName()).String())
		err := c.executeByInstance(ctx, inst)
		if err != nil {
			return err
		}
		msg.Println("")
		msg.Println("")
	}
	return nil
}

func (c *Status) generateEndpointsData(ctx context.Context, instances []*instance.Instance) ([]*svclookup.StatusResponse, []*status.VipBackendsInstance, error) {
	vipBackendInstances := make([]*status.VipBackendsInstance, 0)
	allResponses := make([]*svclookup.StatusResponse, 0)
	for _, inst := range instances {
		vipBackend, err := c.mapVipBackend(ctx, inst.GetUUID())
		if err != nil {
			return nil, nil, fmt.Errorf("unable to get vip backend mapping: %s", err)
		}
		vipBackendInstances = append(vipBackendInstances, &status.VipBackendsInstance{
			Instance:   inst,
			VipBackend: vipBackend,
		})
		responses, err := c.generateResponses(ctx, inst.GetUUID())
		if err != nil {
			return nil, nil, err
		}
		allResponses = append(allResponses, responses...)
	}
	return allResponses, vipBackendInstances, nil
}

func (c *Status) executeEndpointInteract(ctx context.Context, instances []*instance.Instance) error {
	spinner := msg.GetSpinner()
	spinner.Message("Loading...")
	spinner.Start()
	allResponses, vipBackendInstances, err := c.generateEndpointsData(ctx, instances)
	if err != nil {
		spinner.Stop()
		return err
	}
	statusEndpointTui := status.NewTableEndpoints(allResponses, vipBackendInstances)
	app := tui.NewApp(statusEndpointTui)
	app.EnableMouse()
	p := tea.NewProgram(app, tea.WithContext(ctx))
	// the program can end by itself (q), the refresh loop then has to stop with it
	refreshCtx, stopRefresh := context.WithCancel(ctx)
	var refresher sync.WaitGroup
	refresher.Go(func() {
		ticker := time.NewTicker(endpointsRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-refreshCtx.Done():
				return
			case <-ticker.C:
			}
			allResponses, vipBackendInstances, err := c.generateEndpointsData(refreshCtx, instances)
			if err != nil {
				continue
			}
			statusEndpointTui.SetData(allResponses, vipBackendInstances)
			p.Send(tuicommon.ReloadMsg(true))
		}
	})
	spinner.Stop()
	_, err = p.Run()
	stopRefresh()
	refresher.Wait()
	if err != nil {
		return err
	}
	if app.Error() != nil {
		return app.Error()
	}

	return err
}

func (c *Status) generateResponses(ctx context.Context, instanceUuid string) ([]*svclookup.StatusResponse, error) {
	typeUrl := TypeFromShort(c.Type)
	resUuid := ""
	if c.Type != "endpoints" {
		resUuid = c.Identifier.Identifier()
	}
	if c.Identifier.Identifier() != "" && !c.Identifier.IsUUID() && c.Type != "endpoints" {
		res, err := FindFirstTypedByName(ctx, c.client, typeUrl, c.Identifier.Identifier())
		if err != nil {
			return nil, err
		}
		resUuid = helpers.GetIdentifier(res)
	}

	if c.provisioners == nil {
		c.provisioners = map[string]bool{}
	}
	agents := make([]string, 0)
	if c.Agent == "" {
		listAgents, err := c.client.Lookup().ListAgent(ctx, &svclookup.ListAgentRequest{
			InstanceUuid: instanceUuid,
		})
		if err != nil {
			return nil, err
		}
		provisioners := make([]string, 0)
		for _, agent := range listAgents.Agents {
			if agent.GetMode() != agentapi.Agent_PROVISIONER {
				agents = append(agents, agent.UUID)
				continue
			}
			c.provisioners[agent.UUID] = true
			provisioners = append(provisioners, agent.UUID)
		}
		agents = append(agents, provisionersReporting(ctx, c.client, instanceUuid, provisioners, typeUrl)...)
	} else {
		agents = append(agents, c.Agent)
	}

	results := make(chan []*svclookup.StatusResponse, len(agents))
	responses := make([]*svclookup.StatusResponse, 0)
	finished := make(chan bool)
	go func() {
		for result := range results {
			responses = append(responses, result...)
		}
		finished <- true
	}()

	var g errgroup.Group
	g.SetLimit(runtime.GOMAXPROCS(0))
	for _, agent := range agents {
		g.Go(func() error {
			statusResponses, err := c.allStatuses(ctx, statusRequest{
				uuid:      instanceUuid,
				agentUuid: agent,
				typeUrl:   typeUrl,
				resUuid:   resUuid,
			})
			if err != nil {
				results <- []*svclookup.StatusResponse{
					{
						InstanceUuid: instanceUuid,
						AgentUuid:    agent,
						State:        svclookup.StatusResponse_ERRORED,
						Error:        err.Error(),
					},
				}
				return nil
			}
			results <- statusResponses
			return nil
		})
	}
	_ = g.Wait()
	close(results)
	<-finished
	// every call failed with the cancellation, ERRORED rows per agent would hide the interruption
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	slices.SortFunc(responses, func(a, b *svclookup.StatusResponse) int {
		isProvisionerA, isProvisionerB := c.provisioners[a.AgentUuid], c.provisioners[b.AgentUuid]
		if isProvisionerA && !isProvisionerB {
			return 1
		}
		if isProvisionerB && !isProvisionerA {
			return -1
		}
		return cmp.Compare(a.AgentUuid+a.InstanceUuid, b.AgentUuid+b.InstanceUuid)
	})
	return responses, nil
}

// provisionersReporting keeps the provisioners whose resource types hold typeUrl. A provisioner whose types can't be
// read stays in the list so the status call shows its error.
func provisionersReporting(ctx context.Context, client *clients.Client, instanceUuid string, provisioners []string, typeUrl string) []string {
	reporting := make([]string, 0, len(provisioners))
	for _, provisioner := range provisioners {
		types, err := client.Lookup().ListResourceTypesStatus(ctx, &svclookup.ListResourceTypesRequest{
			InstanceUuid: instanceUuid,
			AgentUuid:    provisioner,
		})
		if err != nil || slices.Contains(types.GetResourceTypes(), typeUrl) {
			reporting = append(reporting, provisioner)
		}
	}
	return reporting
}

func (c *Status) isEndpointInteract() bool {
	return c.Type == "endpoints" && msg.Isatty() && !c.NoInteract && !c.Json
}

func (c *Status) executeByInstance(ctx context.Context, inst *instance.Instance) error {

	responses, err := c.generateResponses(ctx, inst.GetUUID())
	if err != nil {
		return err
	}
	if c.Json {
		return PrintProtoListJson[*svclookup.StatusResponse](responses)
	}

	if c.Type == "endpoints" {
		vipBackend, err := c.mapVipBackend(ctx, inst.GetUUID())
		if err != nil {
			return fmt.Errorf("unable to get vip backend mapping: %s", err)
		}
		return c.printEndpoints(vipBackend, responses)
	}

	return c.printAnyResource(responses)
}

func (c *Status) mapVipBackend(ctx context.Context, instanceUuid string) (map[string][]string, error) {
	vipBackend := make(map[string][]string)
	var offset, limit int32 = 0, 100
	var vipsList []*vip.VIP

	filters := []*svccore.ListFilter{
		{
			Key:   helpers.TypeUrl[*instance.Instance](),
			Value: instanceUuid,
		},
	}

	for {
		response, err := c.client.Vip().ListVIP(ctx, &svcvip.ListVIPRequest{
			Limit:   limit,
			Offset:  offset,
			Filters: filters,
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

		for _, vsInfo := range vsList {
			for _, backendUuid := range vsInfo.GetBackendUuids() {
				listVsExp, ok := vipBackend[backendUuid]
				if !ok {
					listVsExp = make([]string, 0)
				}
				ipStr := vipInfo.GetIp()
				if strings.Contains(vipInfo.GetIp(), ":") {
					ipStr = "[" + vipInfo.GetIp() + "]"
				}
				listVsExp = append(listVsExp, fmt.Sprintf("%s:%d/%s",
					ipStr,
					vsInfo.GetPort(),
					vsInfo.GetName(),
				))
				slices.Sort(listVsExp)
				vipBackend[backendUuid] = listVsExp
			}
		}
	}
	return vipBackend, nil
}

func (c *Status) printAndClearAgentErrors(responses []*svclookup.StatusResponse) []*svclookup.StatusResponse {
	removeFromResp := make([]*svclookup.StatusResponse, 0)
	for _, statusResponse := range responses {
		if statusResponse.GetState() == svclookup.StatusResponse_ERRORED &&
			statusResponse.ErroredResource == nil && statusResponse.PreviousResource == nil {
			msg.Errorf(
				"Agent %s in error: %s\n",
				statusResponse.AgentUuid,
				common.RpcErrorFormat(common.RpcErrorFromString(statusResponse.Error)),
			)
			continue
		}
		removeFromResp = append(removeFromResp, statusResponse)
	}
	return removeFromResp
}

func (c *Status) printEndpoints(vipBackend map[string][]string, responses []*svclookup.StatusResponse) error {
	responses = c.printAndClearAgentErrors(responses)
	if len(responses) == 0 {
		return nil
	}
	buf := &bytes.Buffer{}
	tbl := MakeTableWriter([]string{"Agent UUID", "IP:PORT/Virtual Server", "State", "Endpoint", "Endpoint State", "Endpoint Error"}, buf)
	tbl.Options(tableRowLines(), tableMergedColumns(0, 1, 2))
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
				if err := tbl.Append([]string{"", "", "", "", "", ""}); err != nil {
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
			vsExp, ok := vipBackend[endpointStats.GetBackendUuid()]
			if !ok {
				vsExp = []string{"Backend UUID: " + endpointStats.GetBackendUuid()}
			}
			shouldSkip := false
			if c.Identifier.Identifier() != "" {
				shouldSkip = true
				newVsExp := make([]string, 0)
				for _, vsInfo := range vsExp {
					if strings.Contains(vsInfo, c.Identifier.Identifier()) {
						newVsExp = append(newVsExp, vsInfo)
						shouldSkip = false
					}
				}
				vsExp = newVsExp
			}
			if shouldSkip {
				continue
			}
			cols := []string{
				msg.Colorize(statusResponse.AgentUuid, autoColorSess.GetColor(statusResponse.AgentUuid)).String(),
				strings.Join(vsExp, "\n"),
				healthy,
				endpointStat.GetIpAddr().Format(),
				healthyEndpoint,
				endpointStat.GetLastError().GetValue(),
			}
			if err := tbl.Append(cols); err != nil {
				return err
			}
		}
	}

	if err := tbl.Render(); err != nil {
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

func (c *Status) printAnyResource(responses []*svclookup.StatusResponse) error {
	responses = c.printAndClearAgentErrors(responses)
	if len(responses) == 0 {
		return nil
	}
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
			agentLabel(statusResponse.AgentUuid, c.provisioners, autoColorSess),
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

func (c *Status) allStatuses(ctx context.Context, sr statusRequest) ([]*svclookup.StatusResponse, error) {
	if sr.resUuid == "" {
		statusResponses, err := c.client.Lookup().ListStatus(ctx, &svclookup.ListStatusRequest{
			InstanceUuid: sr.uuid,
			AgentUuid:    sr.agentUuid,
			TypeUrl:      sr.typeUrl,
		})
		if err != nil {
			return nil, err
		}
		return statusResponses.GetStatus(), nil
	}
	statusResponse, err := c.client.Lookup().GetStatus(ctx, &svclookup.GetStatusRequest{
		InstanceUuid: sr.uuid,
		AgentUuid:    sr.agentUuid,
		TypeUrl:      sr.typeUrl,
		Identifier:   sr.resUuid,
	})
	if err != nil {
		return nil, err
	}
	return []*svclookup.StatusResponse{statusResponse}, nil
}

// errorAndNotes fills the error column: the error of the resource, then the notes the agent gave on how it applied it
func errorAndNotes(statusResponse *svclookup.StatusResponse) string {
	lines := make([]string, 0, 1+len(statusResponse.GetNotes()))
	if statusResponse.GetError() != "" {
		lines = append(lines, wordwrap.WrapString(statusResponse.GetError(), 60))
	}
	for _, note := range statusResponse.GetNotes() {
		lines = append(lines, noteLine(note))
	}
	return strings.Join(lines, "\n")
}

func noteLine(note *flocktype.Note) string {
	text := note.GetMessage()
	if note.GetScope() != "" {
		text += " (" + note.GetScope() + ")"
	}
	if note.GetLevel() == flocktype.Note_WARNING {
		return msg.Yellow(wordwrap.WrapString("warning: "+text, 60)).String()
	}
	return wordwrap.WrapString("note: "+text, 60)
}
