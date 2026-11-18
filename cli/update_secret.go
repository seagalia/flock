package cli

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	svcsecret "github.com/seagalia/flock/sdk/flock/services/secret/v1"
)

// changedValueMark stands for a new value in the diff, which must never show it.
const changedValueMark = "(changed)"

type UpdateSecret struct {
	Named SecretNamed `arg:"" name:"secret" help:"secret name or uuid" predictor:"secret"`

	Name       string   `short:"n" name:"name" help:"Name"`
	Data       []string `short:"d" name:"data" sep:"none" placeholder:"KEY=VALUE" help:"Add a key or change its value, - as value reads it from stdin (once, then use --force). This can be set multiple times."`
	FromFiles  []string `name:"from-file" sep:"none" placeholder:"KEY=PATH" help:"Add a key or change its value from a file. This can be set multiple times."`
	RemoveKeys []string `name:"remove-key" sep:"none" placeholder:"KEY" help:"Remove a key. This can be set multiple times."`
	Force      bool     `name:"force" help:"Force update secret without confirmation"`

	client *clients.Client
}

func (c *UpdateSecret) SetClient(client *clients.Client) {
	c.client = client
}

func (c *UpdateSecret) Run(ctx context.Context) error {
	current, err := FindSecret(ctx, c.client, &c.Named)
	if err != nil {
		return err
	}

	if err := refuseLocked("secret", current.GetName(), current.GetMetadata()); err != nil {
		return err
	}

	// the confirmation reads its answer from stdin, which a value read from stdin has already consumed
	readsStdin := slices.ContainsFunc(c.Data, func(pair string) bool { return strings.HasSuffix(pair, "="+stdinValue) })
	if readsStdin && !c.Force {
		return fmt.Errorf("a value read from stdin needs --force")
	}

	changes, err := readSecretData(c.Data, c.FromFiles, os.Stdin)
	if err != nil {
		return err
	}

	// A key sent with an empty value keeps its stored value, a key left out is removed.
	data := make(map[string]string, len(current.GetData())+len(changes))
	for key := range current.GetData() {
		data[key] = ""
	}
	for _, key := range c.RemoveKeys {
		if _, exists := current.GetData()[key]; !exists {
			return fmt.Errorf("secret %s has no key %s", current.GetName(), key)
		}
		if _, isChanged := changes[key]; isChanged {
			return fmt.Errorf("key %s is both changed and removed", key)
		}
		delete(data, key)
	}
	maps.Copy(data, changes)

	name := current.GetName()
	if c.Name != "" {
		name = c.Name
	}
	updated := &secret.Secret{UUID: current.GetUUID(), Name: name, Data: data, Metadata: current.GetMetadata()}

	shown := withoutSecretValues(updated)
	for key := range changes {
		shown.Data[key] = changedValueMark
	}
	confirm, err := DiffAndConfirm(withoutSecretValues(current), shown, c.Force)
	if err != nil {
		return err
	}
	if !confirm {
		return nil
	}

	_, err = c.client.Secret().UpdateSecret(ctx, &svcsecret.UpdateSecretRequest{
		UUID:   current.GetUUID(),
		Secret: &secret.Secret{Name: name, Data: data, Metadata: current.GetMetadata()},
	})
	if err != nil {
		return err
	}

	msg.Success("Secret updated successfully.")
	return nil
}
