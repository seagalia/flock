package cli

import (
	"context"
	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
)

type ReadBackend struct {
	Named BackendNamed `arg:"" name:"backend" help:"backend name or uuid" predictor:"backend"`

	Json     bool `short:"j" name:"json" help:"Print the backend in JSON format"`
	ShowUUID bool `name:"uuid" help:"Retrieve and only display the given resource's guid."`

	client *clients.Client
}

func (c *ReadBackend) SetClient(client *clients.Client) {
	c.client = client
}

func (c *ReadBackend) Run(ctx context.Context) error {
	cl, err := FindBackend(ctx, c.client, &c.Named)
	if err != nil {
		return err
	}

	if c.ShowUUID {
		msg.Println(cl.GetUUID())
		return nil
	}

	if c.Json {
		return PrintProtoJson(cl)
	}

	msg.UseStderr()
	msg.Infof("Backend %s configuration", msg.Cyan(c.Named.Identifier()))
	msg.Printf("━━━━━\n")
	msg.UseStdout()
	return PrintProtoHuman(cl)
}
