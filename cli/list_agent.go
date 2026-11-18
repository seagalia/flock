package cli

import (
	"context"
	"fmt"
	"strings"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
)

type ListAgent struct {
	Named InstanceNamed `arg:"" name:"instance" help:"instance name or uuid" predictor:"instance"`

	Json bool `short:"j" name:"json" help:"Format in json instead of human table readable."`

	client *clients.Client
}

func (c *ListAgent) SetClient(client *clients.Client) {
	c.client = client
}

func (c *ListAgent) Run(ctx context.Context) error {
	uuid := c.Named.Identifier()
	if !c.Named.IsUUID() {
		res, err := FindFirstByName[*instance.Instance](ctx, c.client, c.Named.Identifier())
		if err != nil {
			return err
		}
		uuid = res.UUID
	}

	listAgents, err := c.client.Lookup().ListAgent(ctx, &svclookup.ListAgentRequest{
		InstanceUuid: uuid,
	})

	if err != nil {
		return err
	}

	if c.Json {
		return PrintProtoJson(listAgents)
	}

	if len(listAgents.GetAgents()) == 0 {
		msg.Info("No agent found.")
		return nil
	}

	table := MakeTableWriter([]string{"UUID", "Host", "Name", "Type", "Mode", "Has Extensions", "Features"})
	table.Options(tableMergedColumns(3, 4, 5, 6), tableWrap(false))
	for _, agent := range listAgents.GetAgents() {
		agentType := agent.GetType()
		// the flock starling agent announces itself with the type "starling"
		if agentType == "starling" {
			agentType = msg.Green(agentType).String()
		}
		if err := table.Append([]string{msg.Cyan(agent.GetUUID()).String(), agent.GetHost(), agent.GetName(),
			agentType,
			agent.GetMode().String(),
			strings.Join(agent.GetExtensions(), "\n"),
			fmt.Sprintf(`Show logs: %s
Show metrics: %s
Show status: %s
Show L3 log: %s`,
				boolFmtTable(agent.GetFeatures().GetShowLogs()),
				boolFmtTable(agent.GetFeatures().GetShowMetrics()),
				boolFmtTable(agent.GetFeatures().GetShowStatus()),
				boolFmtTable(agent.GetFeatures().GetShowL3Log())),
		}); err != nil {
			return err
		}
	}
	return table.Render()
}
