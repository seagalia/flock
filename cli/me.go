package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/seagalia/flock/cli/app"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	svclogin "github.com/seagalia/flock/sdk/flock/services/login/v1"
	"golang.org/x/oauth2"
	"google.golang.org/protobuf/types/known/emptypb"
)

type UserShare struct {
	Username       string   `json:"username"`
	Groups         []string `json:"groups"`
	Host           string   `json:"host"`
	Http1Transport bool     `json:"http1_transport"`
	Http1Scheme    string   `json:"http1_scheme"`
}

type Me struct {
	Json  bool `short:"j" name:"json" help:"Format in json instead of human table readable."`
	Share bool `short:"s" name:"share" help:"Share your username/groups/endpoint to give to an operator for getting help."`

	client      *clients.Client
	cfg         *app.Config
	tokenSource oauth2.TokenSource
}

func (c *Me) SetClient(client *clients.Client) {
	c.client = client
}

func (c *Me) SetTokenSource(tokenSource oauth2.TokenSource) {
	c.tokenSource = tokenSource
}

func (c *Me) Run(ctx context.Context) error {
	meInfo, err := c.client.Login().Me(ctx, &emptypb.Empty{})
	if err != nil {
		return err
	}
	if c.Share {
		return c.share(meInfo)
	}

	token, err := c.tokenSource.Token()
	if err != nil {
		return err
	}
	// a bearer given with FLOCK_TOKEN has no known expiry
	var expiresAt *time.Time
	if !token.Expiry.IsZero() {
		localExpiry := token.Expiry.In(time.Local)
		expiresAt = &localExpiry
	}
	if c.Json {
		b, err := json.MarshalIndent(struct {
			Username  string     `json:"username"`
			Groups    []string   `json:"groups"`
			IsAdmin   bool       `json:"is_admin"`
			Token     string     `json:"token"`
			ExpiresAt *time.Time `json:"expires_at,omitempty"`
		}{
			Username:  meInfo.Username,
			Groups:    meInfo.Groups,
			IsAdmin:   meInfo.IsAdmin,
			Token:     token.AccessToken,
			ExpiresAt: expiresAt,
		}, "", "  ")
		if err != nil {
			return err
		}
		msg.Println(string(b))
		return nil
	}

	msg.Println("Username:        ", msg.Cyan(meInfo.Username).String())
	msg.Println("Groups:          ", msg.Cyan(strings.Join(meInfo.Groups, ", ")).String())
	msg.Println("Are You admin ?: ", boolFmtTable(meInfo.IsAdmin))
	msg.Println("Token:           ", msg.Cyan(token.AccessToken).String())
	if expiresAt == nil {
		msg.Println("Expires At:      ", msg.Cyan("unknown").String())
		return nil
	}
	msg.Println("Expires At:      ", msg.Cyan(*expiresAt).String())
	return nil
}

func (c *Me) share(meInfo *svclogin.MeResponse) error {
	b, err := json.Marshal(UserShare{
		Username:       meInfo.Username,
		Groups:         meInfo.Groups,
		Host:           c.cfg.Host,
		Http1Transport: c.cfg.Http1Transport,
		Http1Scheme:    c.cfg.Http1Scheme,
	})
	if err != nil {
		return err
	}
	result := base64.StdEncoding.EncodeToString(b)
	msg.Infof("Share this to an operator: %s", msg.Cyan(result).String())
	return nil
}

func (c *Me) SetConfig(cfg *app.Config) {
	c.cfg = cfg
}
