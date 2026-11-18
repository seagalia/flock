package cli

import (
	"context"
	"fmt"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
)

type StatusAgents struct {
	Named InstanceNamed `arg:"" name:"instance" help:"instance name or uuid" predictor:"instance"`

	Json bool `short:"j" name:"json" help:"Format in json instead of human table readable."`

	Agent string `short:"a" name:"agent" help:"UUID of the agent you would like to get status from"`

	ShowAnnouncingVips bool `short:"s" name:"show-announcing-vips" help:"Show announcing vips"`

	client *clients.Client

	provisioners map[string]bool
}

// agentLabel is the uuid of the agent, followed by (provisioner) for a provisioner.
func agentLabel(agentUuid string, provisioners map[string]bool, colors *AutoColor) string {
	label := msg.Colorize(agentUuid, colors.GetColor(agentUuid)).String()
	if provisioners[agentUuid] {
		label += " (provisioner)"
	}
	return label
}

func (c *StatusAgents) SetClient(client *clients.Client) {
	c.client = client
}

func (c *StatusAgents) Run(ctx context.Context) error {
	inst, err := FindInstance(ctx, c.client, &c.Named)
	if err != nil {
		return err
	}
	resp, err := c.client.Lookup().ListAgentStatus(ctx, &svclookup.ListAgentStatusRequest{
		InstanceUuid:       inst.GetUUID(),
		AgentUuid:          c.Agent,
		ShowAnnouncingVips: c.ShowAnnouncingVips,
	})
	if err != nil {
		return err
	}
	if len(resp.GetAgentStatus()) == 0 {
		return fmt.Errorf("No agent found")
	}
	if c.Json {
		return PrintProtoListJson[*svclookup.GetAgentStatusResponse](resp.GetAgentStatus())
	}
	listAgents, err := c.client.Lookup().ListAgent(ctx, &svclookup.ListAgentRequest{
		InstanceUuid: inst.GetUUID(),
	})
	if err != nil {
		return err
	}
	c.provisioners = map[string]bool{}
	for _, a := range listAgents.GetAgents() {
		c.provisioners[a.GetUUID()] = a.GetMode() == agent.Agent_PROVISIONER
	}
	// a provisioner answers a sidecar state whatever the agents of the instance are, and announces no vip
	var agentStatuses, provisionerStatuses []*svclookup.GetAgentStatusResponse
	for _, statusResponse := range resp.GetAgentStatus() {
		if c.provisioners[statusResponse.GetAgentUuid()] {
			provisionerStatuses = append(provisionerStatuses, statusResponse)
			continue
		}
		agentStatuses = append(agentStatuses, statusResponse)
	}
	hasAgents := len(agentStatuses) > 0
	isSidecar := hasAgents && agentStatuses[0].GetAgentStatus().GetSidecarAgentState() != nil
	isProxy := hasAgents && agentStatuses[0].GetAgentStatus().GetProxyAgentState() != nil
	isUnknownType := hasAgents && !isSidecar && !isProxy
	if isUnknownType {
		return fmt.Errorf("unknown agent type")
	}

	autoColorSess := NewAutoColor()
	msg.Println("")
	msg.Info("Agent status and components states:")
	msg.Println("")
	if isProxy {
		err = c.showProxy(agentStatuses, autoColorSess)
		if err != nil {
			return err
		}
		if len(provisionerStatuses) > 0 {
			msg.Println("")
			err = c.showSidecar(provisionerStatuses, autoColorSess)
		}
	} else {
		err = c.showSidecar(append(agentStatuses, provisionerStatuses...), autoColorSess)
	}
	if err != nil {
		return err
	}
	if !c.ShowAnnouncingVips || len(agentStatuses) == 0 {
		return nil
	}
	msg.Println("")
	msg.Info("Announcing VIPs:")
	msg.Println("")
	if isSidecar {
		return c.showVipsSidecar(agentStatuses, autoColorSess)
	}
	return c.showVipsProxy(agentStatuses, autoColorSess)
}

func (c *StatusAgents) colorAgentState(state *agent.State) string {
	switch state.GetStatus() {
	case agent.Status_OK:
		return msg.Green("OK").String()
	case agent.Status_ERRORED:
		return msg.Red("ERRORED").String()
	case agent.Status_WARNING:
		return msg.Yellow("WARNING").String()
	}
	return msg.Yellow("Unknown").String()
}

func (c *StatusAgents) showVipsSidecar(resp []*svclookup.GetAgentStatusResponse, autoColorSess *AutoColor) error {
	table := MakeTableWriter([]string{"Agent UUID", "Vips Announced"})
	table.Options(tableWrap(true), tableRowLines(), tableMergedColumns(0, 1))

	for _, statusResponse := range resp {
		agentState := statusResponse.GetAgentStatus().GetSidecarAgentState()
		if len(agentState.AnnouncingVips) == 0 {
			statusResponse.AgentStatus.GetState()
			if err := table.Append([]string{
				agentLabel(statusResponse.AgentUuid, c.provisioners, autoColorSess),
				"",
			}); err != nil {
				return err
			}
		}
		for _, v := range agentState.AnnouncingVips {
			statusResponse.AgentStatus.GetState()
			if err := table.Append([]string{
				agentLabel(statusResponse.AgentUuid, c.provisioners, autoColorSess),
				v.ToIpNet().String(),
			}); err != nil {
				return err
			}
		}

	}
	return table.Render()
}

func (c *StatusAgents) showVipsProxy(resp []*svclookup.GetAgentStatusResponse, autoColorSess *AutoColor) error {
	table := MakeTableWriter([]string{"Agent UUID", "Component Name", "Vips Announced"})
	table.Options(tableWrap(true), tableRowLines(), tableMergedColumns(0, 1, 2))

	for _, statusResponse := range resp {
		agentState := statusResponse.GetAgentStatus().GetProxyAgentState()
		for _, comp := range agentState.GetComponentAnnouncingVips() {
			if len(comp.AnnouncingVips) == 0 {
				statusResponse.AgentStatus.GetState()
				if err := table.Append([]string{
					agentLabel(statusResponse.AgentUuid, c.provisioners, autoColorSess),
					comp.GetName(),
					"",
				}); err != nil {
					return err
				}
			}
			for _, v := range comp.AnnouncingVips {
				statusResponse.AgentStatus.GetState()
				if err := table.Append([]string{
					agentLabel(statusResponse.AgentUuid, c.provisioners, autoColorSess),
					comp.GetName(),
					v.ToIpNet().String(),
				}); err != nil {
					return err
				}
			}

		}
	}
	return table.Render()
}

func (c *StatusAgents) showSidecar(resp []*svclookup.GetAgentStatusResponse, autoColorSess *AutoColor) error {
	table := MakeTableWriter([]string{"Agent UUID", "State", "Details", "Component", "Component State", "Component Details"})
	table.Options(tableWrap(true), tableRowLines(), tableMergedColumns(0, 1))

	for _, statusResponse := range resp {
		agentState := statusResponse.GetAgentStatus().GetSidecarAgentState()
		agentStateDisp := c.colorAgentState(agentState.GetAgentState())
		statusResponse.AgentStatus.GetState()
		if len(agentState.GetComponentStates()) == 0 {
			if err := table.Append([]string{
				agentLabel(statusResponse.AgentUuid, c.provisioners, autoColorSess),
				agentStateDisp,
				agentState.GetAgentState().GetDetails(),
				"",
				"",
				"",
			}); err != nil {
				return err
			}
			continue
		}
		for _, compState := range agentState.GetComponentStates() {
			if err := table.Append([]string{
				agentLabel(statusResponse.AgentUuid, c.provisioners, autoColorSess),
				agentStateDisp,
				agentState.GetAgentState().GetDetails(),
				compState.GetName(),
				c.colorAgentState(compState),
				compState.GetDetails(),
			}); err != nil {
				return err
			}
		}
	}
	return table.Render()
}

func (c *StatusAgents) showProxy(resp []*svclookup.GetAgentStatusResponse, autoColorSess *AutoColor) error {
	if len(resp) == 0 {
		msg.Error("No agent found")
		return nil
	}
	table := MakeTableWriter([]string{"Server Name", "State", "Details"})
	table.Options(tableWrap(true), tableRowLines())

	for _, statusResponse := range resp {
		agentState := statusResponse.GetAgentStatus().GetProxyAgentState()

		for _, compState := range agentState.GetComponentStates() {
			if err := table.Append([]string{
				msg.Colorize(compState.GetName(), autoColorSess.GetColor(compState.GetName())).String(),
				c.colorAgentState(compState),
				compState.GetDetails(),
			}); err != nil {
				return err
			}
		}
	}
	return table.Render()
}
