package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/cli/app"
	"github.com/seagalia/flock/sdk/clients"
	"google.golang.org/protobuf/types/known/emptypb"
)

type SetAsUser struct {
	FromShare string   `short:"f" name:"from-share" help:"Set as user from a 'flock me --share' given by user."`
	Username  string   `short:"u" name:"username" help:"Set as user from a username. Ignored when use from-share."`
	Groups    []string `short:"g" name:"group" sep:"none" help:"Set as user from a group, you can set multiple time. Ignored when use from-share."`
	Clear     bool     `short:"c" name:"clear" help:"Clear the user set."`

	cfg    *app.Config
	client *clients.Client
}

func (c *SetAsUser) SetClient(client *clients.Client) {
	c.client = client
}

func (c *SetAsUser) SetConfig(config *app.Config) {
	c.cfg = config
}

func (c *SetAsUser) Run(ctx context.Context) error {
	if c.Clear {
		return c.clear()
	}
	if c.FromShare == "" && c.Username == "" && len(c.Groups) == 0 {
		return fmt.Errorf("you need to set a username/groups or use --from-share")
	}
	meInfo, err := c.client.Login().Me(ctx, &emptypb.Empty{})
	if err != nil {
		return err
	}
	if !meInfo.GetIsAdmin() {
		return fmt.Errorf("you need to be admin to use this command")
	}
	if c.FromShare == "" {
		return c.fromUserPassword()
	}

	return c.fromShare()
}

func (c *SetAsUser) fromShare() error {
	b, err := base64.StdEncoding.DecodeString(c.FromShare)
	if err != nil {
		return err
	}
	usrShare := &UserShare{}
	err = json.Unmarshal(b, usrShare)
	if err != nil {
		return err
	}

	if usrShare.Username == "" {
		return fmt.Errorf("username cannot be empty")
	}

	c.cfg.AsUser = &app.AsUser{
		Username: usrShare.Username,
		Groups:   usrShare.Groups,
	}
	otherHost := false
	if c.cfg.Host != usrShare.Host {
		otherHost = true
		c.cfg.Host = usrShare.Host
		c.cfg.Http1Scheme = usrShare.Http1Scheme
		c.cfg.Http1Transport = usrShare.Http1Transport
	}
	err = app.WriteConfig(ExpandConfigPath(), c.cfg)
	if err != nil {
		return err
	}
	if otherHost {
		msg.Info("User is on another flock server, please now use login before using any command.")
	} else {
		msg.Successf(
			"You can now list as user %s with groups %s",
			msg.Cyan(usrShare.Username).String(),
			msg.Cyan(strings.Join(usrShare.Groups, ",")).String(),
		)
	}
	return nil
}

func (c *SetAsUser) fromUserPassword() error {
	if c.Username == "" {
		return fmt.Errorf("username cannot be empty")
	}
	c.cfg.AsUser = &app.AsUser{
		Username: c.Username,
		Groups:   c.Groups,
	}
	err := app.WriteConfig(ExpandConfigPath(), c.cfg)
	if err != nil {
		return err
	}
	msg.Successf(
		"You can now list as user %s with groups %s",
		msg.Cyan(c.Username).String(),
		msg.Cyan(strings.Join(c.Groups, ",")).String(),
	)
	return nil
}

func (c *SetAsUser) clear() error {
	c.cfg.AsUser = nil
	err := app.WriteConfig(ExpandConfigPath(), c.cfg)
	if err != nil {
		return err
	}
	msg.Success("User set cleared.")
	return nil
}
