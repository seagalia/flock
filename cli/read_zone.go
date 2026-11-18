package cli

import (
	"context"
	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
)

type ReadZone struct {
	Named  ZoneNamed `arg:"" name:"zone" help:"zone name" predictor:"zone"`
	Json   bool      `short:"j" name:"json" help:"Print the zone in JSON format"`
	client *clients.Client
}

func (c *ReadZone) SetClient(client *clients.Client) {
	c.client = client
}

func (c *ReadZone) Run(ctx context.Context) error {
	aZone, err := FindZone(ctx, c.client, &c.Named)
	if err != nil {
		return err
	}

	if c.Json {
		return PrintProtoJson(aZone)
	}

	msg.UseStderr()
	msg.Infof("Zone %s configuration", msg.Cyan(c.Named.Identifier()))
	msg.Printf("━━━━━\n")
	msg.UseStdout()
	return PrintProtoHuman(aZone)
}
