package cli

import (
	"context"
	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/cli/cplters"
	"github.com/seagalia/flock/sdk/clients"
)

type ReadInstance struct {
	Named InstanceNamed `arg:"" name:"instance" help:"instance name or uuid" predictor:"instance"`

	Json     bool `short:"j" name:"json" help:"Print the instance in JSON format"`
	Expand   bool `short:"e" name:"expand" help:"Expand the virtual server configuration (only for human readable format)"`
	ShowUUID bool `name:"uuid" help:"Retrieve and only display the given resource's guid."`

	client *clients.Client
}

func (c *ReadInstance) SetClient(client *clients.Client) {
	c.client = client
}

func (c *ReadInstance) Run(ctx context.Context) error {
	ints, err := FindInstance(ctx, c.client, &c.Named)
	if err != nil {
		return err
	}

	if c.ShowUUID {
		msg.Println(ints.GetUUID())
		return nil
	}

	if c.Json {
		return PrintProtoJson(ints)
	}

	msg.UseStderr()
	msg.Infof("Instance %s configuration", msg.Cyan(c.Named.Identifier()))
	msg.Printf("━━━━━\n")
	msg.UseStdout()
	if c.Expand {
		cplter := &cplters.InstanceComplete{
			Instance: ints,
		}
		return PrintCompleter(ctx, c.client, cplter)
	}
	return PrintProtoHuman(ints)
}
