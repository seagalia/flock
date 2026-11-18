package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/seagalia/flock/sdk/agentsdk/stores"

	msg "github.com/ArthurHlt/messages"
	"github.com/Masterminds/semver/v3"
	"github.com/alecthomas/kong"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/rs/zerolog/log"
	f5app "github.com/seagalia/flock/agent/f5/app"
	f5config "github.com/seagalia/flock/agent/f5/config"
	agentapp "github.com/seagalia/flock/agent/starling/app"
	agentconfig "github.com/seagalia/flock/agent/starling/config"
	"github.com/seagalia/flock/cli"
	"github.com/seagalia/flock/cli/updater"
	controllerapp "github.com/seagalia/flock/controller/app"
	controllerconfig "github.com/seagalia/flock/controller/config"
	"github.com/seagalia/flock/devenv"
	provisionerapp "github.com/seagalia/flock/provisioners/app"
	provisionerconfig "github.com/seagalia/flock/provisioners/config"
	svclogin "github.com/seagalia/flock/sdk/flock/services/login/v1"
	"github.com/seagalia/flock/sdk/logging"
	"github.com/willabides/kongplete"
	"go.uber.org/automaxprocs/maxprocs"
)

var (
	version = "0.0.1-dev"
	commit  = "none"
	date    = "unknown"
)

type ControllerServeCmd struct {
	ConfigPath string `required:"" short:"c" help:"path to the configuration file" type:"path" default:"./config.yml"`
}

func (r *ControllerServeCmd) Run(ctx context.Context) error {
	cnf, err := controllerconfig.ReadConfig(r.ConfigPath)
	if err != nil {
		return fmt.Errorf("read config: %s", err)
	}
	appRun, err := controllerapp.NewApp(cnf)
	if err != nil {
		return fmt.Errorf("create app: %s", err)
	}
	return appRun.RunServer(ctx)
}

type ControllerCheckConfigCmd struct {
	ConfigPath string `required:"" short:"c" help:"path to the configuration file" type:"path" default:"./config.yml"`
}

func (r *ControllerCheckConfigCmd) Run() error {
	_, err := controllerconfig.ReadConfigStrict(r.ConfigPath)
	if err != nil {
		return fmt.Errorf("invalid config %s: %s", r.ConfigPath, err)
	}
	fmt.Printf("config %s is valid\n", r.ConfigPath)
	return nil
}

type ControllerAdminTokenCmd struct {
	ConfigPath string        `required:"" short:"c" help:"path to the configuration file" type:"path" default:"./config.yml"`
	Reason     string        `required:"" help:"Why the login service can't be used, written in every log line of the token."`
	TTL        time.Duration `name:"ttl" default:"15m" help:"Lifetime of the token, at most 1h."`
}

// Run signs with the key of the config: being able to read it is what grants the right to sign
func (r *ControllerAdminTokenCmd) Run() error {
	cnf, err := controllerconfig.ReadConfigStrict(r.ConfigPath)
	if err != nil {
		return fmt.Errorf("invalid config %s: %s", r.ConfigPath, err)
	}
	jwtMan, err := svclogin.NewJwtManager(cnf.Server.JwtMaker)
	if err != nil {
		return err
	}
	// a blank reason would leave the warn lines of the token's calls without explanation
	reason := strings.TrimSpace(r.Reason)
	if reason == "" {
		return errors.New("--reason must not be blank")
	}
	token, expiresAt, err := jwtMan.MakeBreakGlassToken(reason, r.TTL)
	if err != nil {
		return err
	}
	fmt.Println(token)
	fmt.Fprintf(os.Stderr, "break-glass token valid until %s, every call made with it is logged\n", expiresAt.Format(time.RFC3339))
	return nil
}

type StarlingAgentStartCmd struct {
	ConfigPath string `required:"" short:"c" help:"path to the configuration file" type:"path" default:"./config.yml"`
}

func (r *StarlingAgentStartCmd) Run(ctx context.Context) error {
	return runAgent(ctx, r.ConfigPath)
}

type StarlingAgentCheckConfigCmd struct {
	ConfigPath string `required:"" short:"c" help:"path to the configuration file" type:"path" default:"./config.yml"`
}

func (r *StarlingAgentCheckConfigCmd) Run() error {
	_, err := agentconfig.ReadConfigStrict(r.ConfigPath)
	if err != nil {
		return fmt.Errorf("invalid config %s: %s", r.ConfigPath, err)
	}
	fmt.Printf("config %s is valid\n", r.ConfigPath)
	return nil
}

type StarlingAgentCleanCmd struct {
	ConfigPath string `required:"" short:"c" help:"path to the configuration file" type:"path" default:"./config.yml"`
}

func (r *StarlingAgentCleanCmd) Run() error {
	cnf, err := agentconfig.ReadConfig(r.ConfigPath)
	if err != nil {
		return fmt.Errorf("read config: %s", err)
	}
	err = logging.Setup(logging.Options{Level: cnf.Log.Level, NoColor: cnf.Log.NoColor, InJson: cnf.Log.InJson})
	if err != nil {
		return fmt.Errorf("setup logging: %s", err)
	}
	return agentapp.CleanHost(cnf)
}

type F5AgentStartCmd struct {
	ConfigPath string `required:"" short:"c" help:"path to the configuration file" type:"path" default:"./config.yml"`
}

func (r *F5AgentStartCmd) Run(ctx context.Context) error {
	setupAgentRuntime()
	cnf, err := f5config.ReadConfig(r.ConfigPath)
	if err != nil {
		return fmt.Errorf("read config: %s", err)
	}
	sversion, err := agentVersion()
	if err != nil {
		return err
	}
	appRun, err := f5app.NewApp(cnf, sversion)
	if err != nil {
		return fmt.Errorf("create app: %s", err)
	}
	return appRun.Run(ctx)
}

type F5AgentCheckConfigCmd struct {
	ConfigPath string `required:"" short:"c" help:"path to the configuration file" type:"path" default:"./config.yml"`
}

func (r *F5AgentCheckConfigCmd) Run() error {
	_, err := f5config.ReadConfigStrict(r.ConfigPath)
	if err != nil {
		return fmt.Errorf("invalid config %s: %s", r.ConfigPath, err)
	}
	fmt.Printf("config %s is valid\n", r.ConfigPath)
	return nil
}

type ProvisionerStartCmd struct {
	ConfigPath string `required:"" short:"c" help:"path to the configuration file" type:"path" default:"./config.yml"`
}

func (r *ProvisionerStartCmd) Run(ctx context.Context) error {
	setupAgentRuntime()
	cnf, err := provisionerconfig.ReadConfig(r.ConfigPath)
	if err != nil {
		return fmt.Errorf("read config: %s", err)
	}
	sversion, err := agentVersion()
	if err != nil {
		return err
	}
	appRun, err := provisionerapp.NewApp(cnf, sversion)
	if err != nil {
		return fmt.Errorf("create app: %s", err)
	}
	// here and not in NewApp: in flock dev the provisioner runs next to the agent, whose store keeps the count
	stores.CountResourcesOf(appRun.Runtime().Store)
	return appRun.Run(ctx)
}

type ProvisionerCheckConfigCmd struct {
	ConfigPath string `required:"" short:"c" help:"path to the configuration file" type:"path" default:"./config.yml"`
}

func (r *ProvisionerCheckConfigCmd) Run() error {
	_, err := provisionerconfig.ReadConfigStrict(r.ConfigPath)
	if err != nil {
		return fmt.Errorf("invalid config %s: %s", r.ConfigPath, err)
	}
	fmt.Printf("config %s is valid\n", r.ConfigPath)
	return nil
}

// setupAgentRuntime was the agent binary's init(); only the agent did it, so the controller
// keeps running without automaxprocs and without the build info collector.
func setupAgentRuntime() {
	maxprocs.Set(maxprocs.Logger(func(format string, args ...any) { log.Trace().Msgf(format, args...) })) //nolint:errcheck
	prometheus.MustRegister(collectors.NewBuildInfoCollector())
}

func runAgent(ctx context.Context, configPath string) error {
	setupAgentRuntime()
	cnf, err := agentconfig.ReadConfig(configPath)
	if err != nil {
		return fmt.Errorf("read config: %s", err)
	}

	sversion, err := agentVersion()
	if err != nil {
		return err
	}
	appRun, err := agentapp.NewApp(cnf, sversion)
	if err != nil {
		return fmt.Errorf("create app: %s", err)
	}
	return appRun.Run(ctx)
}

// agentVersion is the version an agent announces to controller: the build version with the short commit
func agentVersion() (*semver.Version, error) {
	shortCommit := commit
	if len(shortCommit) > 7 {
		shortCommit = shortCommit[:7]
	}
	sversion, err := semver.NewVersion(fmt.Sprintf("%s+%s", version, shortCommit))
	if err != nil {
		return nil, fmt.Errorf("semver parse error: %s", err)
	}
	return sversion, nil
}

type DevCmd struct {
	EnvoyVersion  string   `name:"envoy-version" help:"Envoy release downloaded on linux when ~/.flock/bin/envoy is missing" default:"1.40.0"`
	NoBrowser     bool     `name:"no-browser" help:"Don't open the dashboard in a browser once flock is ready"`
	NoProvisioner bool     `name:"no-provisioner" help:"Don't start the provisioner (acme certificates), only the controller and the agent"`
	LogLevel      string   `name:"log-level" help:"Log level of every app flock dev runs, they share the logging of the process" default:"info" enum:"trace,debug,info,warn,error"`
	DataDir       string   `name:"data-dir" help:"Where flock dev keeps its certificates, configs, store and generated admin password" default:"~/.flock/dev" env:"FLOCK_DEV_DATA_DIR"`
	ListenHost    string   `name:"listen-host" help:"Address the controller and its dashboard listen on, anything but loopback requires a password" default:"127.0.0.1" env:"FLOCK_DEV_LISTEN_HOST"`
	Hostnames     []string `name:"hostname" help:"Name or IP added to the controller certificate, repeatable" env:"FLOCK_DEV_HOSTNAMES"`
	Privileged    bool     `name:"privileged" help:"Run the real starling agent (IPVS, nftables, flock0) instead of its dev mode, linux only, changes this machine" env:"FLOCK_DEV_PRIVILEGED"`
	Yes           bool     `name:"yes" help:"Agree in advance to what --privileged asks" env:"FLOCK_DEV_YES"`
	EnvoyPath     string   `name:"envoy-path" help:"Envoy binary to run, nothing is downloaded when set" env:"FLOCK_DEV_ENVOY_PATH"`
}

func (r *DevCmd) Run(ctx context.Context) error {
	setupAgentRuntime()
	sversion, err := agentVersion()
	if err != nil {
		return err
	}
	dataDir, err := cli.ExpandHome(r.DataDir)
	if err != nil {
		return err
	}
	return devenv.Run(ctx, devenv.Options{
		Version: sversion, EnvoyVersion: r.EnvoyVersion, NoBrowser: r.NoBrowser, NoProvisioner: r.NoProvisioner,
		LogLevel: r.LogLevel, DataDir: dataDir, ListenHost: r.ListenHost, Hostnames: r.Hostnames,
		Privileged: r.Privileged, Yes: r.Yes, EnvoyPath: r.EnvoyPath, AdminPassword: os.Getenv("FLOCK_ADMIN_PASSWORD"),
	})
}

type VersionCmd struct {
}

func (l *VersionCmd) Run() error { //nolint:unparam // kong requires Run to return an error
	fmt.Println(versionLine())
	return nil
}

func versionLine() string {
	return fmt.Sprintf("flock %s, commit %s, built at %s", version, commit, date)
}

type ControllerCmd struct {
	Serve       ControllerServeCmd       `cmd:"" help:"Run server."`
	CheckConfig ControllerCheckConfigCmd `cmd:"" name:"check-config" help:"Check a configuration file, unknown keys included, without running the controller."`
	AdminToken  ControllerAdminTokenCmd  `cmd:"" name:"admin-token" help:"Sign a short-lived admin token with the controller's jwt_maker key, for when the login service is unusable."`
}

type AgentCmd struct {
	Starling StarlingAgentCmd `cmd:"" help:"Run the starling agent, the load balancer on its own host."`
	F5       F5AgentCmd       `cmd:"" name:"f5" help:"Run the F5 BIG-IP bridge agent."`
}

type StarlingAgentCmd struct {
	Start       StarlingAgentStartCmd       `cmd:"" help:"Run handler and serves services."`
	Clean       StarlingAgentCleanCmd       `cmd:"" help:"Remove what the agent left on its host, without contacting the controller."`
	CheckConfig StarlingAgentCheckConfigCmd `cmd:"" name:"check-config" help:"Check a configuration file, unknown keys included, without running the agent."`
}

type F5AgentCmd struct {
	Start       F5AgentStartCmd       `cmd:"" help:"Receive the resources of the controller and declare them on the BIG-IP with AS3."`
	CheckConfig F5AgentCheckConfigCmd `cmd:"" name:"check-config" help:"Check a configuration file, unknown keys included, without running the agent."`
}

type ProvisionerCmd struct {
	Start       ProvisionerStartCmd       `cmd:"" help:"Receive the resources of the controller and provision what they ask for, like ACME certificates."`
	CheckConfig ProvisionerCheckConfigCmd `cmd:"" name:"check-config" help:"Check a configuration file, unknown keys included, without running the provisioner."`
}

// CLI is the root of the flock command tree: the cli commands live at the root, the server commands
// (controller, agent, provisioner, version) beside them.
type CLI struct {
	cli.Commands `embed:""`

	Controller  ControllerCmd  `cmd:"" group:"server" help:"Run the controller."`
	Agent       AgentCmd       `cmd:"" group:"server" help:"Run an agent: starling, or f5 for BIG-IP."`
	Provisioner ProvisionerCmd `cmd:"" group:"server" help:"Run the provisioner, which writes back to the controller what resources ask for (ACME certificates)."`
	Dev         DevCmd         `cmd:"" group:"server" help:"Run a local controller and its agents with no configuration, to try flock. A dev mode, not for production: there, run each component in its own container with its own config."`
	Version     VersionCmd     `cmd:"" group:"server" help:"Show version."`
}

func newParser(c *CLI, options ...kong.Option) (*kong.Kong, error) {
	groups := append([]kong.Group{}, cli.HelpGroups...)
	groups = append(groups, kong.Group{Key: "server", Title: "Server"})
	baseOptions := []kong.Option{
		kong.Name("flock"),
		// the full help of all the commands would bury the error
		kong.ShortUsageOnError(),
		kong.ExplicitGroups(groups),
	}
	return kong.New(c, slices.Concat(baseOptions, options)...)
}

// isServerCommand tells the commands that run the controller, an agent or the provisioner: they need no
// client and no update check.
func isServerCommand(ctx *kong.Context) bool {
	serverCommands := []string{"controller", "agent", "provisioner", "dev", "version"}
	firstWord, _, _ := strings.Cut(ctx.Command(), " ")
	return slices.Contains(serverCommands, firstWord)
}

func run(ctx context.Context, kctx *kong.Context, c *CLI) error {
	if isServerCommand(kctx) {
		return kctx.Run()
	}
	return cli.Run(ctx, kctx, &c.Commands, updater.BinInfo{Version: version, Commit: commit, Date: date})
}

// execute parses args, runs the selected command and prints its error, exiting through parser.Exit.
func execute(parser *kong.Kong, c *CLI, args []string) {
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// the first signal cancels rootCtx and gives the default behavior back, so a second one kills the process even
	// when the shutdown hangs
	context.AfterFunc(rootCtx, stop)

	kctx, err := parser.Parse(args)
	if err != nil {
		parser.FatalIfErrorf(err)
		return
	}
	kctx.BindTo(rootCtx, (*context.Context)(nil))
	err = run(rootCtx, kctx, c)
	if err == nil {
		return
	}
	if isServerCommand(kctx) {
		kctx.FatalIfErrorf(err)
		return
	}
	// cli commands print errors like the rest of their output, with the messages library
	msg.Error(err.Error())
	parser.Exit(1)
}

func main() {
	c := &CLI{}
	parser, err := newParser(c)
	if err != nil {
		panic(err)
	}
	kongplete.Complete(parser, cli.CompletionPredictors()...)

	execute(parser, c, os.Args[1:])
}
