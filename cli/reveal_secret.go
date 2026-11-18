package cli

import (
	"context"
	"fmt"
	"slices"

	"github.com/seagalia/flock/sdk/clients"
	core "github.com/seagalia/flock/sdk/flock/services/core/v1"
)

type RevealSecret struct {
	Named SecretNamed `arg:"" name:"secret" help:"secret name or uuid" predictor:"secret"`

	Key  string `short:"k" name:"key" help:"Only print the value of this key, with no key= prefix."`
	Json bool   `short:"j" name:"json" help:"Format in json instead of human table readable."`

	client *clients.Client
}

func (c *RevealSecret) SetClient(client *clients.Client) {
	c.client = client
}

func (c *RevealSecret) Run(ctx context.Context) error {
	found, err := FindSecret(ctx, c.client, &c.Named)
	if err != nil {
		return err
	}
	response, err := c.client.Secret().RevealSecret(ctx, &core.UuidRequest{UUID: found.GetUUID()})
	if err != nil {
		return err
	}
	revealed := response.GetSecret()

	if c.Key != "" {
		value, exists := revealed.GetData()[c.Key]
		if !exists {
			return fmt.Errorf("secret %s has no key %s", found.GetName(), c.Key)
		}
		if c.Json {
			revealed.Data = map[string]string{c.Key: value}
			return PrintProtoJson(revealed)
		}
		fmt.Println(value)
		return nil
	}

	if c.Json {
		return PrintProtoJson(revealed)
	}
	keys := make([]string, 0, len(revealed.GetData()))
	for key := range revealed.GetData() {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		fmt.Printf("%s=%s\n", key, revealed.GetData()[key])
	}
	return nil
}
