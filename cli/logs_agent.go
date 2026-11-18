package cli

import (
	"context"
	"fmt"

	msg "github.com/ArthurHlt/messages"
	"github.com/logrusorgru/aurora/v4"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
)

type Logs struct {
	Named InstanceNamed `arg:"" name:"instance" help:"instance name or uuid" predictor:"instance"`

	Agent  string `short:"a" name:"agent" help:"UUID of the agent you would like to get logs from"`
	Latest bool   `short:"l" name:"latest" help:"Get latest logs (follow them by default)"`

	client *clients.Client
}

func (c *Logs) SetClient(client *clients.Client) {
	c.client = client
}

func (c *Logs) Run(ctx context.Context) error {

	uuid := c.Named.Identifier()
	if !c.Named.IsUUID() {
		res, err := FindFirstByName[*instance.Instance](ctx, c.client, c.Named.Identifier())
		if err != nil {
			return err
		}
		uuid = res.UUID
	}

	if !c.Latest {
		msg.Infof("Following logs of instance %s ...", msg.Cyan(c.Named.Identifier()))
		return c.followLogs(ctx, uuid)
	}

	latestLog, err := c.client.Lookup().GetLogsLatest(ctx, &svclookup.GetLogsRequest{
		InstanceUuid: uuid,
		AgentUuid:    c.Agent,
	})
	if err != nil {
		return err
	}
	currentColor := uint(0)
	colorMap := map[string]aurora.Color{}

	for _, log := range latestLog.GetLogs() {
		if _, ok := colorMap[log.GetAgentUuid()]; !ok {
			colorMap[log.GetAgentUuid()] = auroraColorMap[currentColor]
			currentColor++
			currentColor = currentColor % 7
		}
		for _, line := range log.GetLogLines() {
			fmt.Print(c.format(log.GetAgentUuid(), line, colorMap[log.GetAgentUuid()]))
		}
	}

	return nil
}

func (c *Logs) followLogs(ctx context.Context, instUuid string) error {
	stream, err := c.client.Lookup().GetLogsStream(ctx, &svclookup.GetLogsRequest{
		InstanceUuid: instUuid,
		AgentUuid:    c.Agent,
	})
	if err != nil {
		return err
	}
	defer stream.Close() //nolint:errcheck
	autoColorSess := NewAutoColor()
	for {
		log, err := stream.Receive()
		if err != nil {
			// Ctrl-C ends following, so the command succeeds
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		line := log.GetLogStream()
		if line == nil {
			continue // the started message, before the first line
		}
		fmt.Print(c.format(line.GetAgentUuid(), line.GetLogLine(), autoColorSess.GetColor(line.GetAgentUuid())))
	}
}

func (c *Logs) format(agentUuid, logLine string, color aurora.Color) string {
	agentPrefix := fmt.Sprintf("Agent %s ", msg.Colorize(agentUuid, color))
	return fmt.Sprintf("%s   %s", msg.Italic(msg.BgGray(8, agentPrefix)), logLine)
}
