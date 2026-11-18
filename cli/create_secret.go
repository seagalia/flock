package cli

import (
	"context"
	"os"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	svcsecret "github.com/seagalia/flock/sdk/flock/services/secret/v1"
)

type CreateSecret struct {
	Name      string   `short:"n" name:"name" help:"Name of the secret" required:""`
	Data      []string `short:"d" name:"data" sep:"none" placeholder:"KEY=VALUE" help:"A key of the secret and its value, - as value reads it from stdin (once). This can be set multiple times."`
	FromFiles []string `name:"from-file" sep:"none" placeholder:"KEY=PATH" help:"A key of the secret and the file holding its value. This can be set multiple times."`
	Json      bool     `short:"j" name:"json" help:"Format in json instead of human table readable."`

	client *clients.Client
}

func (c *CreateSecret) SetClient(client *clients.Client) {
	c.client = client
}

func (c *CreateSecret) Run(ctx context.Context) error {
	data, err := readSecretData(c.Data, c.FromFiles, os.Stdin)
	if err != nil {
		return err
	}
	response, err := c.client.Secret().CreateSecret(ctx, &svcsecret.CreateSecretRequest{
		Secret: &secret.Secret{Name: c.Name, Data: data},
	})
	if err != nil {
		return err
	}
	if c.Json {
		return PrintProtoJson(withoutSecretValues(response.GetSecret()))
	}
	msg.Successf("Secret %s successfully.", msg.Green("created"))
	return nil
}

func withoutSecretValues(s *secret.Secret) *secret.Secret {
	blanked := &secret.Secret{UUID: s.GetUUID(), Name: s.GetName(), Metadata: s.GetMetadata(), Data: map[string]string{}}
	for key := range s.GetData() {
		blanked.Data[key] = ""
	}
	return blanked
}
