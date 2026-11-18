package cli

import (
	"context"
	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/cli/cplters"
	"github.com/seagalia/flock/sdk/clients"
)

type ReadVip struct {
	Named VipNamed `arg:"" name:"vip" help:"vip name or uuid" predictor:"vip"`

	Expand   bool `short:"e" name:"expand" help:"Expand the virtual server configuration (only for human readable format)"`
	Json     bool `short:"j" name:"json" help:"Print the vip in JSON format"`
	ShowUUID bool `name:"uuid" help:"Retrieve and only display the given resource's guid."`

	client *clients.Client
}

func (c *ReadVip) SetClient(client *clients.Client) {
	c.client = client
}

func (c *ReadVip) Run(ctx context.Context) error {
	aVip, err := FindVIP(ctx, c.client, &c.Named)
	if err != nil {
		return err
	}

	if c.ShowUUID {
		msg.Println(aVip.GetUUID())
		return nil
	}

	if c.Json {
		return PrintProtoJson(aVip)
	}

	msg.UseStderr()
	msg.Infof("Vip %s configuration", msg.Cyan(c.Named.Identifier()))
	msg.Printf("━━━━━\n")
	msg.UseStdout()
	if c.Expand {
		cplter := &cplters.VIPComplete{
			VIP: aVip,
		}
		return PrintCompleter(ctx, c.client, cplter)
	}
	return PrintProtoHuman(aVip)
}
