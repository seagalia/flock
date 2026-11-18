package cli

import (
	"context"
	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
)

type ReadSecret struct {
	Named SecretNamed `arg:"" name:"secret" help:"secret name or uuid" predictor:"secret"`

	Json     bool `short:"j" name:"json" help:"Format in json instead of human table readable."`
	ShowUUID bool `name:"uuid" help:"Retrieve and only display the given resource's guid."`

	client *clients.Client
}

func (c *ReadSecret) SetClient(client *clients.Client) {
	c.client = client
}

func (c *ReadSecret) Run(ctx context.Context) error {
	found, err := FindSecret(ctx, c.client, &c.Named)
	if err != nil {
		return err
	}

	if c.ShowUUID {
		msg.Println(found.GetUUID())
		return nil
	}

	shown := withoutSecretValues(found)
	if c.Json {
		return PrintProtoJson(shown)
	}
	msg.UseStderr()
	msg.Infof("Secret %s configuration\n", msg.Cyan(c.Named.Identifier()))
	msg.Printf("━━━━━\n")
	msg.UseStdout()
	return PrintProtoHuman(shown)
}
