// Package devenv runs `flock dev`: a controller and its agents (a starling agent in dev mode, a provisioner) in one
// process, with no configuration, so that flock can be tried on a laptop in one command.
package devenv

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	msg "github.com/ArthurHlt/messages"
	"github.com/Masterminds/semver/v3"
	"github.com/charmbracelet/x/term"
	"github.com/pkg/browser"
	agentapp "github.com/seagalia/flock/agent/starling/app"
	"github.com/seagalia/flock/cli"
	controllerapp "github.com/seagalia/flock/controller/app"
	controllerconfig "github.com/seagalia/flock/controller/config"
	"github.com/seagalia/flock/webui"
)

const (
	controllerStartTimeout = 30 * time.Second
	loginSeedTimeout       = time.Minute
	dashboardUrl           = "http://" + dashboardAddr + "/dashboard/"
)

type Options struct {
	// Version is the agent version announced to controller
	Version *semver.Version
	// EnvoyVersion is the envoy release downloaded on linux when envoy is missing
	EnvoyVersion string
	// NoBrowser keeps the dashboard closed once flock is ready, for a session with no desktop
	NoBrowser bool
	// NoProvisioner runs the controller and the agent only
	NoProvisioner bool
	// LogLevel is the log level of every app of flock dev, they share the logging of the process
	LogLevel string
	// DataDir, with ~ already expanded, keeps the certificates, the configs, the store and the generated admin password
	DataDir string
	// ListenHost is where the controller listens, anything but a loopback address exposes it and needs a real password
	ListenHost string
	// Hostnames are added to the controller certificate, the names clients outside this machine reach it with
	Hostnames []string
	// Privileged runs the real starling agent (IPVS, nftables, flock0) instead of its dev mode
	Privileged bool
	// Yes agrees in advance to the changes the privileged mode makes on the machine
	Yes bool
	// EnvoyPath is the envoy to run, nothing is installed when set
	EnvoyPath string
	// AdminPassword replaces the admin password flock dev would use or generate
	AdminPassword string
}

// Run starts the controller, seeds it, starts the provisioner and the agent, and returns once all of them have stopped,
// which happens when ctx is cancelled (SIGINT, SIGTERM) or when one of them stops by itself.
// The controller store (kept across sessions), the certificates and the configs live in opts.DataDir, an installed
// envoy in ~/.flock/bin.
func Run(ctx context.Context, opts Options) error {
	if runtime.GOOS == "windows" {
		return errors.New("flock dev is not supported on windows")
	}
	printBanner(os.Stdout)
	fmt.Println()

	err := confirmStart(ctx, opts)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	envoyPath, err := installEnvoy(ctx, opts)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}

	setup, err := prepareController(ctx, opts)
	if err != nil {
		return err
	}
	certs, password, controllerAddr := setup.certs, setup.password, setup.controllerAddr
	pidPath, err := writePidFile()
	if err != nil {
		return err
	}
	defer os.Remove(pidPath) // nolint:errcheck

	controller, err := controllerapp.NewApp(setup.controllerCnf)
	if err != nil {
		return fmt.Errorf("create controller: %w", err)
	}

	// any app stopping cancels runCtx so the others stop with it, and Run returns once every started app is done
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var apps sync.WaitGroup
	// the first error of an app that stopped on its own, a stop asked through ctx is not one
	var firstErr error
	var errOnce sync.Once
	startApp := func(name string, run func(context.Context) error) {
		apps.Go(func() {
			defer cancel()
			err := run(runCtx)
			if runCtx.Err() != nil {
				return
			}
			// an app returning while nobody asked it to stop leaves flock dev half alive, so it is reported
			if err == nil {
				err = errors.New("stopped unexpectedly")
			}
			errOnce.Do(func() { firstErr = fmt.Errorf("%s: %w", name, err) })
		})
	}
	// stops the apps and waits for them, whatever the way out
	finish := func(err error) error {
		cancel()
		apps.Wait()
		if firstErr != nil {
			return firstErr
		}
		// a stop asked before flock dev was ready is a clean exit
		if ctx.Err() != nil {
			return nil
		}
		return err
	}

	controllerStopped := make(chan struct{})
	startApp("controller", func(ctx context.Context) error {
		defer close(controllerStopped)
		return controller.RunServer(ctx)
	})
	err = waitController(runCtx, controllerAddr, controllerStopped)
	if err != nil {
		return finish(err)
	}

	cliConfigPath, err := cli.ExpandHome(cli.DevCliConfigPath)
	if err != nil {
		return finish(err)
	}
	seedCtx, cancelSeed := context.WithTimeout(runCtx, loginSeedTimeout)
	defer cancelSeed()
	client, err := loginAdmin(seedCtx, cliConfigPath, controllerAddr, password)
	if err != nil {
		return finish(fmt.Errorf("login admin: %w", err))
	}
	instanceUuid, err := seed(seedCtx, client)
	if err != nil {
		return finish(err)
	}

	var snatIfaces []string
	if opts.Privileged {
		snatIfaces, err = listSnatIfaces()
		if err != nil {
			return finish(fmt.Errorf("interfaces to source-nat on: %w", err))
		}
	}
	agentCnf, err := writeAgentConfig(certs, agentSettings{
		SnatIfaces:     snatIfaces,
		InstanceUuid:   instanceUuid,
		EnvoyPath:      envoyPath,
		LogLevel:       opts.LogLevel,
		ControllerAddr: controllerAddr,
		Privileged:     opts.Privileged,
	}, filepath.Join(opts.DataDir, "agent.yml"))
	if err != nil {
		return finish(fmt.Errorf("agent config: %w", err))
	}
	// before the agent: both set up the logging of the process, the last one wins and the agent's is the one to keep
	provisionerStarted := false
	if !opts.NoProvisioner {
		waitProvisioner := startProvisioner(runCtx, certs, controllerAddr, instanceUuid, filepath.Join(opts.DataDir, "provisioner.yml"), opts.LogLevel, opts.Version)
		provisionerStarted = waitProvisioner != nil
		if provisionerStarted {
			apps.Go(func() {
				waitProvisioner()
			})
		}
	}
	printReady(opts, envoyPath, password, provisionerStarted)
	if !opts.NoBrowser && webui.IsBuilt() {
		openDashboard(password)
	}

	agent, err := agentapp.NewApp(agentCnf, opts.Version)
	if err != nil {
		return finish(fmt.Errorf("create agent: %w", err))
	}
	if opts.Privileged {
		// the agent changed the host (nftables, flock0, IPVS), a stop must undo it
		agent.CleanOnCtxDone()
	}
	startApp("agent", agent.Run)
	<-runCtx.Done()
	return finish(nil)
}

// devSetup is what the controller needs and the other apps share, made before any of them starts.
type devSetup struct {
	certs          *pki
	password       string
	controllerAddr string
	controllerCnf  *controllerconfig.Config
}

func prepareController(ctx context.Context, opts Options) (*devSetup, error) {
	err := os.MkdirAll(opts.DataDir, 0700)
	if err != nil {
		return nil, fmt.Errorf("data dir: %w", err)
	}
	exposed := isExposed(opts.ListenHost)
	names, err := controllerNames(opts.Hostnames, exposed)
	if err != nil {
		return nil, err
	}
	certs, err := ensurePki(filepath.Join(opts.DataDir, "pki"), names)
	if err != nil {
		return nil, fmt.Errorf("certificates: %w", err)
	}
	password, err := resolveAdminPassword(opts.DataDir, exposed, opts.AdminPassword)
	if err != nil {
		return nil, err
	}
	controllerAddr := internalControllerAddr(opts.ListenHost)
	err = checkPortFree(ctx, controllerAddr)
	if err != nil {
		return nil, err
	}
	controllerCnf, err := writeControllerConfig(certs, controllerSettings{
		StorePath:     filepath.Join(opts.DataDir, "store.db"),
		LogLevel:      opts.LogLevel,
		ListenAddr:    net.JoinHostPort(opts.ListenHost, controllerPort),
		AdminPassword: password,
	}, filepath.Join(opts.DataDir, "controller.yml"))
	if err != nil {
		return nil, fmt.Errorf("controller config: %w", err)
	}
	return &devSetup{certs: certs, password: password, controllerAddr: controllerAddr, controllerCnf: controllerCnf}, nil
}

// writePidFile tells the cli commands to use the cli config of this session, as long as the file exists.
func writePidFile() (string, error) {
	pidPath, err := cli.ExpandHome(cli.DevPidPath)
	if err != nil {
		return "", err
	}
	// ~/.flock/dev holds the session files even when the data dir is elsewhere
	err = os.MkdirAll(filepath.Dir(pidPath), 0700)
	if err != nil {
		return "", err
	}
	err = os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0644)
	if err != nil {
		return "", err
	}
	return pidPath, nil
}

// confirmStart shows the not for production warning and, in privileged mode, checks the machine allows it and asks the
// user to agree with what it changes.
func confirmStart(ctx context.Context, opts Options) error {
	err := warnNotForProduction(ctx, os.Stdout)
	if err != nil {
		return err
	}
	if !opts.Privileged {
		return nil
	}
	err = checkPrivileged()
	if err != nil {
		return err
	}
	return confirmPrivileged(ctx, os.Stdin, term.IsTerminal(os.Stdin.Fd()), os.Stdout, opts.Yes)
}

func installEnvoy(ctx context.Context, opts Options) (string, error) {
	flockDir, err := cli.ExpandHome("~/.flock")
	if err != nil {
		return "", err
	}
	envoyVersion := opts.EnvoyVersion
	if envoyVersion == "" {
		envoyVersion = defaultEnvoyVersion
	}
	return resolveEnvoy(ctx, filepath.Join(flockDir, "bin"), envoyVersion, opts.EnvoyPath)
}

// controllerNames gives the names added to the controller certificate. An exposed controller is also reached on the
// addresses of the machine, by clients that know no name for it.
func controllerNames(hostnames []string, exposed bool) ([]string, error) {
	names := make([]string, 0, len(hostnames))
	for _, hostname := range hostnames {
		names = append(names, strings.TrimSpace(hostname))
	}
	if !exposed {
		return names, nil
	}
	ips, err := localIPs()
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		names = append(names, ip.String())
	}
	return names, nil
}

// openDashboard opens the login page with the admin credentials filled in, and asks the page to offer its tour after
// the login. They travel in the fragment, which the browser never sends to the server, and the page removes it from
// the address once read.
func openDashboard(password string) {
	loginUrl := dashboardUrl + "login#username=" + adminUser + "&password=" + url.QueryEscape(password) + "&tour=offer"
	// a missing browser only costs the click on the url of the banner
	err := browser.OpenURL(loginUrl)
	if err != nil {
		msg.Warningf("the dashboard could not be opened in a browser: %v\n", err)
	}
}

// checkPortFree refuses to start on an address something already answers on. The bind alone can't tell: macOS lets
// 127.0.0.1:8443 be bound next to a wildcard listener (a docker published port), then the cli and the apps reach two
// controllers that don't share their jwt keys.
func checkPortFree(ctx context.Context, addr string) error {
	dialer := net.Dialer{Timeout: time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil
	}
	_ = conn.Close()
	return fmt.Errorf("something already answers on %s (another flock dev, a container publishing the port), stop it first", addr)
}

// waitController returns once controller accepts connections on controllerAddr, or with the error that stopped it
func waitController(ctx context.Context, controllerAddr string, controllerStopped <-chan struct{}) error {
	deadline := time.Now().Add(controllerStartTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-controllerStopped:
			return errors.New("controller stopped before it listened")
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		dialer := net.Dialer{Timeout: time.Second}
		conn, err := dialer.DialContext(ctx, "tcp", controllerAddr)
		if err == nil {
			return conn.Close()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("controller not listening on %s after %s", controllerAddr, controllerStartTimeout)
}

func printReady(opts Options, envoyPath, password string, withProvisioner bool) {
	exposed := isExposed(opts.ListenHost)
	if opts.Privileged {
		msg.Infof("IPVS serves tcp and udp, envoy serves http and tls (%s)\n", envoyPath)
	} else {
		msg.Warningf("dev mode: envoy is started (%s) and IPVS is not used, every virtual server (http, tls, tcp, udp) is served by envoy\n", envoyPath)
	}
	msg.Success("flock is ready, the cli is logged in, run in another terminal:")
	msg.Printf("  %s       %s\n", msg.Cyan("flock me"), "who you are")
	msg.Printf("  %s     %s\n", msg.Cyan("flock vips"), fmt.Sprintf("the vip %q on every address of the machine", vipName))
	msg.Printf("  %s %s\n", msg.Cyan("flock create-vs -f vs.yml"), "serve your first virtual server")
	controllerAddr := internalControllerAddr(opts.ListenHost)
	dashboard := dashboardUrl
	if exposed {
		controllerAddr = net.JoinHostPort(externalHostnames(opts.Hostnames)[0], controllerPort)
		dashboard = "https://" + controllerAddr + "/dashboard/"
	}
	msg.Printf("Controller %s, admin %s/%s, instance %q, envoy admin http://127.0.0.1:19000\n",
		controllerAddr, adminUser, password, instanceName)
	msg.Printf("Dashboard %s, log in as %s/%s\n", msg.Cyan(dashboard), adminUser, password)
	if exposed {
		msg.Println("Log in from another machine, the cli inside this machine is already logged in:")
		for _, hostname := range externalHostnames(opts.Hostnames) {
			msg.Printf("  %s\n", msg.Cyan(loginCommand(hostname, password)))
		}
	}
	if withProvisioner {
		msg.Printf("Provisioner %q on %s (acme on Let's Encrypt staging), its config is %s\n",
			provisionerName, provisionerHost, filepath.Join(opts.DataDir, "provisioner.yml"))
	}
	if !webui.IsBuilt() {
		msg.Warning("this flock binary has no dashboard, run make webui then rebuild flock\n")
	}
	msg.Printf("Certificates and configs are in %s, stop with Ctrl-C.\n\n", opts.DataDir)
}

// loginCommand gives the login line users paste in a POSIX shell, hence the quoted password
func loginCommand(hostname, password string) string {
	quoted := "'" + strings.ReplaceAll(password, "'", `'\''`) + "'"
	passwordFlag := "-p " + quoted
	// kong reads a value starting with a dash as a flag, quoting can't help since the shell removes the quotes
	if strings.HasPrefix(password, "-") {
		passwordFlag = "--password=" + quoted
	}
	return fmt.Sprintf("flock login -t %s -u %s %s -k", net.JoinHostPort(hostname, controllerPort), adminUser, passwordFlag)
}

// externalHostnames gives the names clients outside this machine use, localhost when none is set: what a docker
// `-p 8443:8443` publishes.
func externalHostnames(hostnames []string) []string {
	if len(hostnames) == 0 {
		return []string{"localhost"}
	}
	return hostnames
}
