package cli

import (
	"context"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
)

type AddVipToInstance struct {
	Named InstanceNamed `arg:"" name:"instance" help:"instance name or uuid" predictor:"instance"`
	Vip   VipNamed      `arg:"" name:"vip" help:"vip name or uuid" predictor:"vip"`

	client *clients.Client
}

func (c *AddVipToInstance) SetClient(client *clients.Client) {
	c.client = client
}

func (c *AddVipToInstance) Run(ctx context.Context) error {
	anInstance, err := FindInstance(ctx, c.client, &c.Named)
	if err != nil {
		return err
	}

	if err := refuseLocked("instance", anInstance.GetName(), anInstance.GetMetadata()); err != nil {
		return err
	}

	aVip, err := FindVIP(ctx, c.client, &c.Vip)
	if err != nil {
		return err
	}

	_, err = c.client.Instance().AddVipToInstance(ctx, &svcinstance.AddVipToInstanceRequest{
		UUID:    anInstance.GetUUID(),
		VipUuid: aVip.GetUUID(),
	})
	if err != nil {
		return err
	}
	msg.Successf("Vip successfully %s.", msg.Green("added"))

	return nil
}
