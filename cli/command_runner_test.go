package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	msg "github.com/ArthurHlt/messages"
	"github.com/alecthomas/kong"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/cli/app"
	"github.com/seagalia/flock/cli/internal/fakeconnect"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	"golang.org/x/oauth2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const resourceUuid = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"

var sessionExpiry = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// commandRunner runs CLI commands through the real parser, like the flock binary does,
// but with a fake controller instead of a connection built from the user config.
type commandRunner struct {
	conn        *fakeconnect.Conn
	config      *app.Config
	tokenSource oauth2.TokenSource
	configPath  string
	// ctx is the context the commands run on, like the root context of the binary. A spec cancels it to stop a command.
	ctx    context.Context
	cancel context.CancelFunc
	// dialedHosts lists the hosts of connections the command opened itself, like login does.
	dialedHosts []string
}

func newCommandRunner() *commandRunner {
	dir := GinkgoT().TempDir()
	isolateEnvironment(dir)
	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)
	return &commandRunner{
		ctx:         ctx,
		cancel:      cancel,
		conn:        fakeconnect.New(),
		config:      &app.Config{Host: "flock.local:443"},
		tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "session-token", Expiry: sessionExpiry}),
		configPath:  filepath.Join(dir, "config.json"),
	}
}

// run parses args like the flock binary does and returns everything printed on stdout and stderr, without colors.
func (r *commandRunner) run(args ...string) (string, error) {
	GinkgoHelper()
	previousNewClient := app.NewClient
	app.NewClient = func(cnf *app.Config, interceptors ...connect.ClientInterceptor) (*connect.Client, error) {
		r.dialedHosts = append(r.dialedHosts, cnf.Host)
		return r.conn.Client(interceptors...), nil
	}
	defer func() { app.NewClient = previousNewClient }()

	// The config flag keeps commands that read or write the config away from the user's real one.
	argsWithConfig := append([]string{"--cli-config", r.configPath}, args...)
	var err error
	output := captureOutput(func() {
		err = r.parseAndRun(argsWithConfig)
	})
	return output, err
}

func (r *commandRunner) parseAndRun(args []string) error {
	GinkgoHelper()
	commands := &Commands{}
	// The parser is built inside the output capture: kong keeps the stdout it finds at creation.
	parser, err := kong.New(commands, kong.Name("flock"), kong.UsageOnError(), kong.ExplicitGroups(HelpGroups), kong.Exit(func(int) {}))
	Expect(err).NotTo(HaveOccurred(), "invalid command tree")
	kctx, err := parser.Parse(args)
	if err != nil {
		return err
	}
	connectToFake := func() (*connect.Client, oauth2.TokenSource, *app.Config, error) {
		return r.conn.Client(), r.tokenSource, r.config, nil
	}
	err = prepareCommand(r.ctx, commands, kctx.Selected().Target.Addr().Interface(), connectToFake)
	if err != nil {
		return err
	}
	kctx.BindTo(r.ctx, (*context.Context)(nil))
	return kctx.Run()
}

// isolateEnvironment keeps the developer's flock variables out of the commands, they are read as flag
// defaults and config overrides, and moves the home and cache dirs where login stores its session files.
func isolateEnvironment(tempDir string) {
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "FLOCK_") {
			GinkgoT().Setenv(name, "")
			Expect(os.Unsetenv(name)).To(Succeed())
		}
	}
	GinkgoT().Setenv("HOME", tempDir)
	GinkgoT().Setenv("XDG_CACHE_HOME", filepath.Join(tempDir, ".cache"))
}

// captureOutput redirects stdout and stderr to a pipe, commands write to both
// through fmt, tablewriter and the messages package.
func captureOutput(run func()) string {
	GinkgoHelper()
	reader, writer, err := os.Pipe()
	Expect(err).NotTo(HaveOccurred())
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = writer, writer
	msg.UseStdout()
	defer func() {
		os.Stdout, os.Stderr = stdout, stderr
		msg.UseStderr()
	}()

	captured := make(chan string)
	go func() {
		content, _ := io.ReadAll(reader)
		captured <- string(content)
	}()
	run()
	Expect(writer.Close()).To(Succeed())
	return ansiEscape.ReplaceAllString(<-captured, "")
}

func reply(resp proto.Message) fakeconnect.Handler {
	return func(proto.Message) (proto.Message, error) {
		return resp, nil
	}
}

func failWith(err error) fakeconnect.Handler {
	return func(proto.Message) (proto.Message, error) {
		return nil, err
	}
}

// firstPage answers a paginated list call: commands keep listing until they get an empty page.
func firstPage(resp proto.Message) fakeconnect.Handler {
	return func(req proto.Message) (proto.Message, error) {
		isNextPage := req.(interface{ GetOffset() int32 }).GetOffset() > 0
		if isNextPage {
			return resp.ProtoReflect().New().Interface(), nil
		}
		return resp, nil
	}
}

// parseJSON decodes command output: protojson output can't be matched as text, its whitespace is randomized on purpose.
func parseJSON(out string) any {
	GinkgoHelper()
	var decoded any
	Expect(json.Unmarshal([]byte(out), &decoded)).To(Succeed(), "output is not json: %s", out)
	return decoded
}

func lastRequest[T proto.Message](conn *fakeconnect.Conn, method string) T {
	GinkgoHelper()
	requests := conn.Requests(method)
	Expect(requests).NotTo(BeEmpty(), "no call to %s", method)
	return requests[len(requests)-1].(T)
}

func sentFilters(runner *commandRunner, method string) []*svccore.ListFilter {
	GinkgoHelper()
	return lastRequest[proto.Message](runner.conn, method).(interface {
		GetFilters() []*svccore.ListFilter
	}).GetFilters()
}

func packAny(message proto.Message) *anypb.Any {
	GinkgoHelper()
	packed, err := anypb.New(message)
	Expect(err).NotTo(HaveOccurred())
	return packed
}

func tcpVs(uuid, name string, port uint32, backendUuid string) *vs.VirtualServer {
	return &vs.VirtualServer{UUID: uuid, Name: name, Port: port, RouteConfig: &vs.VirtualServer_TcpRouteConfig{
		TcpRouteConfig: &vs.TcpRouteConfig{BackendUuid: backendUuid},
	}}
}

// endpointStats is a healthy backend with one unhealthy endpoint, shown as "Warning".
var endpointStats = &backend.BackendEndpointStats{
	BackendUuid: "backend-1",
	Healthy:     true,
	EndpointStats: []*backend.EndpointStat{
		{IpAddr: &core.IPAddr{Ip: "10.0.0.5", Port: 8080}, Healthy: true},
		{IpAddr: &core.IPAddr{Ip: "10.0.0.6", Port: 8080}, Healthy: false, LastError: wrapperspb.String("connection refused")},
	},
}
