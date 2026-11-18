package cli

import (
	"context"
	"strings"

	"charm.land/huh/v2"
	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/cli/app"
	"github.com/seagalia/flock/cli/tui/tuicommon"
	svclogin "github.com/seagalia/flock/sdk/flock/services/login/v1"
)

type LoginUser struct {
	ClientId          string `short:"i" name:"client_id" help:"OIDC client id, logs a machine in with client credentials" env:"FLOCK_CLIENT_ID"`
	ClientSecret      string `short:"e" name:"client_secret" help:"OIDC client secret of --client_id" env:"FLOCK_CLIENT_SECRET"`
	Username          string `short:"u" name:"username" help:"Username" env:"FLOCK_USERNAME"`
	Password          string `short:"p" name:"password" help:"Password" env:"FLOCK_PASSWORD"`
	Host              string `short:"t" name:"host" help:"Host of flock controller" env:"FLOCK_HOST"`
	Http1             bool   `short:"1" name:"http1" help:"Use http1 transport" env:"FLOCK_HTTP1"`
	Http1Scheme       string `short:"s" name:"http1-scheme" help:"Http1 scheme" enum:"http,https" default:"https" env:"FLOCK_HTTP1_SCHEME"`
	SkipSslValidation bool   `short:"k" name:"skip-ssl-validation" help:"Skip SSL validation" env:"FLOCK_SKIP_SSL_VALIDATION"`
	Device            bool   `name:"device" help:"OIDC: log in with a code entered on another device, for hosts without a browser"`
	CallbackPort      int    `name:"callback-port" help:"OIDC: local port the provider redirects the browser to, http://127.0.0.1:<port>/callback" default:"8000"`
}

func (c *LoginUser) Run(ctx context.Context) error {
	hostSplit := strings.Split(c.Host, ":")
	if c.Host != "" && len(hostSplit) == 1 {
		c.Host = c.Host + ":443"
	}
	cnf := &app.Config{
		Host:           c.Host,
		SkipVerify:     c.SkipSslValidation,
		Http1Transport: c.Http1,
		Http1Scheme:    c.Http1Scheme,
	}
	app.MergeConfig(ExpandConfigPath(), cnf)
	sa, err := app.GetServiceAuth(ctx, cnf)
	if err != nil {
		return err
	}
	if sa.GetType() == "OIDC" {
		return c.loginOidc(ctx, cnf, sa.GetOidc())
	}

	err = c.askCredentials(cnf)
	if err != nil {
		return err
	}
	// without a session file, the client logs in with the credentials of cnf and stores the new token
	app.ClearLogin(ExpandConfigPath(), cnf)
	_, _, err = app.CreateClient(ctx, ExpandConfigPath(), cnf, false)
	if err != nil {
		return err
	}
	msg.Success("Login successful.")
	return nil
}

func (c *LoginUser) loginOidc(ctx context.Context, cnf *app.Config, provider *svclogin.OidcProvider) error {
	if c.Username != "" || c.Password != "" {
		msg.Warning("The controller logs in with OIDC, --username and --password are ignored")
	}
	cnf.ClientId = c.ClientId
	cnf.ClientSecret = c.ClientSecret
	err := app.LoginOidc(ctx, cnf, provider, c.Device, c.CallbackPort)
	if err != nil {
		return err
	}
	_, _, err = app.CreateClient(ctx, ExpandConfigPath(), cnf, false)
	if err != nil {
		return err
	}
	msg.Success("Login successful.")
	return nil
}

// askCredentials prompts the username and the password not given by flags or environment
func (c *LoginUser) askCredentials(cnf *app.Config) error {
	username := c.Username
	password := c.Password
	fields := make([]huh.Field, 0)
	if username == "" {
		username = app.GetCurrentUsername(ExpandConfigPath())
		fields = append(fields, huh.NewInput().Title("Username:").Value(&username))
	}
	if password == "" {
		fields = append(fields, huh.NewInput().Title("Password:").EchoMode(huh.EchoModePassword).Value(&password))
	}
	if len(fields) > 0 {
		err := huh.NewForm(huh.NewGroup(fields...)).WithTheme(tuicommon.FormTheme).Run()
		if err != nil {
			return err
		}
	}
	cnf.Username = username
	cnf.Password = password
	return nil
}
