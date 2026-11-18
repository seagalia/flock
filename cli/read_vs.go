package cli

import (
	"context"
	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/cli/cplters"
	"github.com/seagalia/flock/sdk/clients"
)

type ReadVs struct {
	Named VsNamed `arg:"" name:"virtual-server" help:"virtual server name or uuid" predictor:"vs"`

	Expand   bool `short:"e" name:"expand" help:"Expand the virtual server configuration (only for human readable format)"`
	Json     bool `short:"j" name:"json" help:"Print the virtual server in JSON format"`
	ShowUUID bool `name:"uuid" help:"Retrieve and only display the given resource's guid."`

	client *clients.Client
}

func (c *ReadVs) SetClient(client *clients.Client) {
	c.client = client
}

func (c *ReadVs) Run(ctx context.Context) error {
	virtServ, err := FindVirtServ(ctx, c.client, &c.Named)
	if err != nil {
		return err
	}

	if c.ShowUUID {
		msg.Println(virtServ.GetUUID())
		return nil
	}

	if c.Json {
		return PrintProtoJson(virtServ)
	}

	msg.UseStderr()
	msg.Infof("Virtual Server %s configuration", msg.Cyan(c.Named.Identifier()))
	msg.Printf("━━━━━\n")
	msg.UseStdout()

	if c.Expand {
		cplter := &cplters.VirtualServerComplete{
			VS: virtServ,
		}
		return PrintCompleter(ctx, c.client, cplter)
	}

	return PrintProtoHuman(virtServ)
}
