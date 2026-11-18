// Command wafctl registers and unregisters WAF configs through the client of the agent. The waf suite runs it in the
// WAF container, since the unix socket of the WAF can't be reached from the host.
package main

import (
	"context"
	"time"

	"github.com/alecthomas/kong"
	"github.com/seagalia/flock/agent/starling/waf/client"
	svcregister "github.com/seagalia/flock/sdk/starling_waf/services/register/v1"
)

type RegisterCmd struct {
	Identifier string `arg:"" help:"Value of the x-waf-uuid metadata the envoy listener sends."`
	DetectOnly bool   `xor:"config" required:"" help:"Use the CRS WAF in detection only mode."`
	Rules      string `xor:"config" required:"" help:"Custom rules, a whole coraza configuration."`
}

func (r *RegisterCmd) Run(wafClient *client.Client) error {
	req := &svcregister.RegisterWafConfigRequest{Identifier: r.Identifier}
	if r.DetectOnly {
		req.Config = &svcregister.RegisterWafConfigRequest_DetectOnly{DetectOnly: true}
	} else {
		req.Config = &svcregister.RegisterWafConfigRequest_CustomRules{CustomRules: r.Rules}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := wafClient.RegisterWafConfig(ctx, req)
	return err
}

type UnregisterCmd struct {
	Identifier string `arg:"" help:"Identifier given to register."`
}

func (u *UnregisterCmd) Run(wafClient *client.Client) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := wafClient.UnregisterWafConfig(ctx, &svcregister.UnregisterWafConfigRequest{Identifier: u.Identifier})
	return err
}

type CLI struct {
	SocketPath string        `short:"s" default:"/tmp/waf.sock" help:"Unix socket of the WAF."`
	Register   RegisterCmd   `cmd:"" help:"Register a WAF config."`
	Unregister UnregisterCmd `cmd:"" help:"Unregister a WAF config."`
}

func main() {
	var cli CLI
	ctx := kong.Parse(&cli)
	wafClient, err := client.NewClient(cli.SocketPath)
	ctx.FatalIfErrorf(err)
	defer wafClient.Close()
	ctx.FatalIfErrorf(ctx.Run(wafClient))
}
