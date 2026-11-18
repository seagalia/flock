package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"connectrpc.com/connect/v2"
	msg "github.com/ArthurHlt/messages"
	"github.com/alecthomas/kong"
	"github.com/seagalia/flock/cli/app"
	"github.com/seagalia/flock/cli/common"
	"github.com/seagalia/flock/cli/updater"
	"github.com/seagalia/flock/sdk/clients"
	"golang.org/x/oauth2"
)

const defaultConfigPath = "~/.flock/config.json"

// errInterrupted is what a command reports when the root context ended before it was done, for example on Ctrl-C.
var errInterrupted = errors.New("interrupted")

var isUuidRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}\b-[0-9a-fA-F]{4}\b-[0-9a-fA-F]{4}\b-[0-9a-fA-F]{4}\b-[0-9a-fA-F]{12}$`)

// HelpGroups are the help sections of the commands, the root parser declares them with kong.ExplicitGroups.
var HelpGroups = []kong.Group{
	{Key: "resources", Title: "Resources"},
	{Key: "permissions", Title: "Permissions"},
	{Key: "status", Title: "Status & logs"},
	{Key: "auth", Title: "Auth & session"},
	{Key: "tools", Title: "Tools"},
}

// Commands are the commands of the flock cli, mounted at the root of the flock command tree.
type Commands struct {
	CliConfig string `name:"cli-config" help:"Path to the cli config file (not the controller or agent one)" default:"~/.flock/config.json" env:"FLOCK_CLI_CONFIG"`
	Verbose   bool   `short:"v" name:"verbose" help:"Show verbose output"`

	// kong prints the help sections in the order of their first command: the fields stay grouped
	// by section, in the order of HelpGroups.
	Apply                  ApplyCmd               `cmd:"" name:"apply" group:"resources" help:"Create or update the virtual servers, backends and vips of resource files."`
	AddPoolToZone          AddPoolToZone          `cmd:"" name:"add-pool-to-zone" group:"resources" help:"(Admin Only) Add a pool of ips to a zone."`
	AddVipToInstance       AddVipToInstance       `cmd:"" name:"add-vip-to-instance" group:"resources" help:"Add a vip to an instance, its ip is taken in the zone of the instance."`
	CreateCertificate      CreateCertificate      `cmd:"" name:"create-certificate" group:"resources" help:"Create a certificate."`
	CreateSecret           CreateSecret           `cmd:"" name:"create-secret" group:"resources" help:"Create a secret."`
	CreateBackend          CreateBackend          `cmd:"" name:"create-backend" group:"resources" help:"Create a backend."`
	CreateInstance         CreateInstance         `cmd:"" name:"create-instance" group:"resources" help:"Create an instance."`
	CreateVip              CreateVip              `cmd:"" name:"create-vip" group:"resources" help:"(Admin Only) Create a vip"`
	CreateVs               CreateVs               `cmd:"" name:"create-vs" group:"resources" help:"Create a virtual server."`
	CreateZone             CreateZone             `cmd:"" name:"create-zone" group:"resources" help:"(Admin Only) Create a zone"`
	DeleteCertificate      DeleteCertificate      `cmd:"" name:"delete-certificate" group:"resources" help:"Delete a certificate"`
	DeleteSecret           DeleteSecret           `cmd:"" name:"delete-secret" group:"resources" help:"Delete a secret"`
	DeleteBackend          DeleteBackend          `cmd:"" name:"delete-backend" group:"resources" help:"Delete a backend."`
	DeleteInstance         DeleteInstance         `cmd:"" name:"delete-instance" group:"resources" help:"Delete an instance."`
	DeletePoolFromZone     DeletePoolFromZone     `cmd:"" name:"delete-pool-from-zone" group:"resources" help:"(Admin Only) Delete a pool of ips from a zone."`
	DeleteVip              DeleteVip              `cmd:"" name:"delete-vip" group:"resources" aliases:"dvip" help:"(Admin Only) Delete a vip."`
	DeleteVipFromInstance  DeleteVipFromInstance  `cmd:"" name:"delete-vip-from-instance" group:"resources" help:"Remove a vip from an instance, its ip is released in the zone of the instance."`
	DeleteVs               DeleteVs               `cmd:"" name:"delete-vs" group:"resources" help:"Delete a virtual server."`
	DeleteZone             DeleteZone             `cmd:"" name:"delete-zone" group:"resources" aliases:"dzone" help:"(Admin Only) Delete a zone."`
	Agents                 ListAgent              `cmd:"" name:"agents" group:"resources" aliases:"a" help:"List agent by instance name or uuid."`
	Certificates           ListCertificate        `cmd:"" name:"certificates" group:"resources" aliases:"certs" help:"List certificates"`
	Secrets                ListSecret             `cmd:"" name:"secrets" group:"resources" help:"List secrets"`
	Backends               ListBackend            `cmd:"" name:"backends" group:"resources" aliases:"c" help:"List all backends."`
	Instances              ListInstance           `cmd:"" name:"instances" group:"resources" aliases:"i" help:"List all instances."`
	Vips                   ListVip                `cmd:"" name:"vips" group:"resources" help:"List all vips."`
	VirtualServers         ListVs                 `cmd:"" name:"virtual-servers" group:"resources" aliases:"list-vs,v,vss" help:"List all virtual servers."`
	Zones                  ListZone               `cmd:"" name:"zones" group:"resources" help:"List all zones."`
	Certificate            ReadCertificate        `cmd:"" name:"certificate" group:"resources" help:"Read a certificate"`
	Secret                 ReadSecret             `cmd:"" name:"secret" group:"resources" help:"Read a secret, its values stay hidden"`
	RevealSecret           RevealSecret           `cmd:"" name:"reveal-secret" group:"resources" help:"Print the values of a secret (owner only)"`
	Backend                ReadBackend            `cmd:"" name:"backend" group:"resources" help:"Read a backend."`
	Instance               ReadInstance           `cmd:"" name:"instance" group:"resources" help:"Read a instance."`
	Vip                    ReadVip                `cmd:"" name:"vip" group:"resources" help:"Read a vip."`
	VirtualServer          ReadVs                 `cmd:"" name:"virtual-server" group:"resources" aliases:"vs" help:"Read a virtual server."`
	Zone                   ReadZone               `cmd:"" name:"zone" group:"resources" help:"Read a Zone."`
	RegisterVip            RegisterVip            `cmd:"" name:"register-vip" group:"resources" help:"Register a vip."`
	UpdateCertificate      UpdateCertificate      `cmd:"" name:"update-certificate" group:"resources" help:"Update a certificate."`
	UpdateSecret           UpdateSecret           `cmd:"" name:"update-secret" group:"resources" help:"Update a secret."`
	UpdateBackend          UpdateBackend          `cmd:"" name:"update-backend" group:"resources" help:"Update a backend."`
	UpdateInstance         UpdateInstance         `cmd:"" name:"update-instance" group:"resources" help:"Update an instance."`
	UpdatePoolOfZone       UpdatePoolOfZone       `cmd:"" name:"update-pool-of-zone" group:"resources" help:"(Admin Only) Update a pool of ips of a zone, matched by its name."`
	UpdateVip              UpdateVip              `cmd:"" name:"update-vip" group:"resources" help:"Update a vip"`
	UpdateVs               UpdateVs               `cmd:"" name:"update-vs" group:"resources" help:"Update a virtual server."`
	UpdateZone             UpdateZone             `cmd:"" name:"update-zone" group:"resources" help:"Update a zone"`
	AddPermission          AddPermission          `cmd:"" name:"add-permission" group:"permissions" help:"Add a permission to a member."`
	AllowAllReadPermission AllowAllReadPermission `cmd:"" name:"allow-all-read-permission" group:"permissions" help:"Allow all read permission."`
	DeletePermission       DeletePermission       `cmd:"" name:"delete-permission" group:"permissions" help:"Delete a permission."`
	HasPermission          HasPermission          `cmd:"" name:"has-permission" group:"permissions" help:"Check if a member has a permission."`
	Permissions            ListPermission         `cmd:"" name:"permissions" group:"permissions" aliases:"p" help:"List permissions for a given resource."`
	Logs                   Logs                   `cmd:"" name:"logs" group:"status" aliases:"l" help:"Get logs from an instance or a specific agent."`
	ShowInstanceStatus     ShowInstanceStatus     `cmd:"" name:"show-instance-status" group:"status" aliases:"sis" help:"show instance status."`
	Status                 Status                 `cmd:"" name:"status" group:"status" aliases:"s" help:"Get status of resource type in all or one agent"`
	StatusCertificates     StatusCertificates     `cmd:"" name:"status-certificates" group:"status" help:"Get status of certificates in all or one agent"`
	StatusBackends         StatusBackends         `cmd:"" name:"status-backends" group:"status" help:"Get status of backends in all or one agent"`
	StatusVips             StatusVips             `cmd:"" name:"status-vips" group:"status" help:"Get status of vips in all or one agent"`
	StatusVs               StatusVs               `cmd:"" name:"status-vs" group:"status" help:"Get status of virtual servers in all or one agent"`
	StatusEndpoints        StatusEndpoints        `cmd:"" name:"status-endpoints" group:"status" help:"Get status of endpoints in all or one agent"`
	StatusAgents           StatusAgents           `cmd:"" name:"status-agents" group:"status" aliases:"sa" help:"Get status of one or all agents of an instance"`
	ClearSession           ClearSessionCmd        `cmd:"" name:"clear-session" group:"auth" help:"Clear current token and session."`
	Login                  LoginUser              `cmd:"" name:"login" group:"auth" help:"Login"`
	Me                     Me                     `cmd:"" name:"me" group:"auth" help:"Get information about your current user."`
	SetAsUser              SetAsUser              `cmd:"" name:"set-as-user" group:"auth" aliases:"sau" help:"(Admin Only) Set as user to be able to list like a user"`
	Ai                     AiCmd                  `cmd:"" name:"ai" group:"tools" help:"Help AI agents use flock."`
	Check                  Check                  `cmd:"" name:"check" group:"tools" help:"check"`
	Dashboard              DashboardCmd           `cmd:"" name:"dashboard" group:"tools" help:"Serve the web dashboard, with the session of the cli (local mode) or for users who sign in (standalone mode)."`
	Explain                Explain                `cmd:"" name:"explain" group:"tools" help:"Explain the fields of a resource or an extension, offline."`
	Help                   HelpCmd                `cmd:"" name:"help" group:"tools" help:"Show this help message"`
	Hook                   Hook                   `cmd:"" name:"hook" group:"tools" help:"Hook your shell for using flock with autocompletion."`
	Overview               Overview               `cmd:"" name:"overview" group:"tools" aliases:"ovv" help:"Show an overview of all flock."`
	SelfUpdate             SelfUpdate             `cmd:"" name:"self-update" group:"tools" help:"Auto-update flock cli"`
	ShowLinkedResource     ShowLinkedResource     `cmd:"" name:"show-linked-resource" group:"tools" aliases:"slr" help:"Show linked resource of any resource"`
	Validate               Validate               `cmd:"" name:"validate" group:"tools" help:"Check a resource file offline, without the controller."`
}

type SetClient interface {
	SetClient(client *clients.Client)
}

type SetConfig interface {
	SetConfig(config *app.Config)
}

type NoCheckUpdateCommand interface {
	NoCheckUpdate() bool
}

type SetTokenSource interface {
	SetTokenSource(tokenSource oauth2.TokenSource)
}

type connector func() (*connect.Client, oauth2.TokenSource, *app.Config, error)

var binInfoGlobal updater.BinInfo

var configPath = defaultConfigPath

func checkUpdate(ctx context.Context) {
	msg.Debug("Checking update ...")
	// The answer is cached next to the cli config so GitHub is asked once a day.
	cachePath := filepath.Join(filepath.Dir(ExpandConfigPath()), "update-check.json")
	needUpdate, ver, err := updater.IsNeedUpdate(ctx, binInfoGlobal, cachePath)
	if err != nil {
		msg.Debugf("Error when checking update: %s", err)
	}
	if needUpdate {
		msg.Warningf("New version %s is available, please run %s",
			msg.Cyan(ver.String()),
			msg.Cyan("flock self-update"),
		)
	}
	msg.Debug("Checking update ... done")
}

// CheckForUpdate is a variable so specs can count the checks.
var CheckForUpdate = checkUpdate

// Run runs the command selected by kctx: it gives the command its client and config, runs it
// and formats the rpc errors.
func Run(ctx context.Context, kctx *kong.Context, commands *Commands, binInfo updater.BinInfo) error {
	binInfoGlobal = binInfo
	updater.RemoveReplacedBinary()
	connectFromConfig := func() (*connect.Client, oauth2.TokenSource, *app.Config, error) {
		return app.CreateClientFromFile(ctx, ExpandConfigPath(), false)
	}
	err := prepareCommand(ctx, commands, kctx.Selected().Target.Addr().Interface(), connectFromConfig)
	if err != nil {
		return err
	}

	err = kctx.Run()
	if err != nil {
		if ctx.Err() != nil && errors.Is(err, context.Canceled) {
			return errInterrupted
		}
		return common.RpcErrorFormat(err)
	}
	return nil
}

// prepareCommand gives the selected command what it needs before it runs: a client, the token source and the config.
func prepareCommand(ctx context.Context, commands *Commands, command any, connectController connector) error {
	if commands.Verbose {
		msg.SetVerbose(true)
	}
	configPath = commands.CliConfig
	// an explicit --cli-config or FLOCK_CLI_CONFIG keeps priority over the dev session
	usesDefaultConfig := commands.CliConfig == defaultConfigPath
	if usesDefaultConfig && devSessionRunning() {
		configPath = DevCliConfigPath
		msg.Debug("flock dev is running, using its cli config " + DevCliConfigPath)
	}
	msg.UseStdout()

	var configLoaded *app.Config
	if withClient, ok := command.(SetClient); ok {
		client, tokenSource, cnf, err := connectController()
		if err != nil {
			return err
		}
		configLoaded = cnf
		withClient.SetClient(clients.New(client))
		if withTokenSource, ok := command.(SetTokenSource); ok {
			withTokenSource.SetTokenSource(tokenSource)
		}
	}
	if withConfig, ok := command.(SetConfig); ok {
		withConfig.SetConfig(configLoaded)
	}

	if noCheck, ok := command.(NoCheckUpdateCommand); !ok || !noCheck.NoCheckUpdate() {
		CheckForUpdate(ctx)
	}
	return nil
}

func ExpandConfigPath() string {
	cp, err := ExpandHome(configPath)
	if err != nil {
		panic(fmt.Sprintf("Error while expanding config path %s", err.Error()))
	}
	return cp
}

// ExpandHome replaces a leading ~ of path by the home directory of the user
func ExpandHome(path string) (string, error) {
	isInHome := path == "~" || strings.HasPrefix(path, "~/")
	if !isInHome {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, path[1:]), nil
}
