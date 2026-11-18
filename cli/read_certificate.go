package cli

import (
	"context"
	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
)

type ReadCertificate struct {
	Named CertNamed `arg:"" name:"certificate" help:"cert name or uuid" predictor:"certificate"`

	Json     bool `short:"j" name:"json" help:"Format in json instead of human table readable."`
	ShowUUID bool `name:"uuid" help:"Retrieve and only display the given resource's guid."`

	client *clients.Client
}

func (c *ReadCertificate) SetClient(client *clients.Client) {
	c.client = client
}

func (c *ReadCertificate) Run(ctx context.Context) error {
	crt, err := FindCertificate(ctx, c.client, &c.Named)
	if err != nil {
		return err
	}

	if c.ShowUUID {
		msg.Println(crt.GetUUID())
		return nil
	}

	if c.Json {
		return PrintProtoJson(crt)
	}
	msg.UseStderr()
	msg.Infof("Certificate %s configuration\n", msg.Cyan(c.Named.Identifier()))
	msg.Printf("━━━━━\n")
	msg.UseStdout()
	return PrintProtoHuman(crt)
}
