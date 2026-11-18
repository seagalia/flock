package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	msg "github.com/ArthurHlt/messages"
	"github.com/alecthomas/kong"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/cli"
	controllerconfig "github.com/seagalia/flock/controller/config"
	svclogin "github.com/seagalia/flock/sdk/flock/services/login/v1"
)

// parse runs the real flock grammar on args without exiting the test process
func parse(args ...string) (*CLI, *kong.Context, error) {
	c := &CLI{}
	parser, err := newParser(c, kong.Exit(func(int) {}))
	Expect(err).ToNot(HaveOccurred())
	ctx, err := parser.Parse(args)
	return c, ctx, err
}

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// executeArgs runs args like main does and returns what it printed, without colors, and the exit
// code it asked for (-1 when it did not exit).
func executeArgs(args ...string) (string, int) {
	GinkgoHelper()
	reader, writer, err := os.Pipe()
	Expect(err).ToNot(HaveOccurred())
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

	exitCode := -1
	c := &CLI{}
	// built inside the capture: kong keeps the stdout it finds at creation
	parser, err := newParser(c, kong.Exit(func(code int) { exitCode = code }))
	Expect(err).ToNot(HaveOccurred())
	execute(parser, c, args)

	Expect(writer.Close()).To(Succeed())
	return ansiEscape.ReplaceAllString(<-captured, ""), exitCode
}

var _ = Describe("flock cli", func() {
	It("parses controller serve", func() {
		c, ctx, err := parse("controller", "serve", "-c", "/etc/flock/c.yml")
		Expect(err).ToNot(HaveOccurred())
		Expect(ctx.Command()).To(Equal("controller serve"))
		Expect(c.Controller.Serve.ConfigPath).To(Equal("/etc/flock/c.yml"))
	})

	It("parses agent starling start with --config-path", func() {
		c, ctx, err := parse("agent", "starling", "start", "--config-path", "/etc/flock/a.yml")
		Expect(err).ToNot(HaveOccurred())
		Expect(ctx.Command()).To(Equal("agent starling start"))
		Expect(c.Agent.Starling.Start.ConfigPath).To(Equal("/etc/flock/a.yml"))
	})

	It("parses agent starling clean with -c", func() {
		c, ctx, err := parse("agent", "starling", "clean", "-c", "/etc/flock/a.yml")
		Expect(err).ToNot(HaveOccurred())
		Expect(ctx.Command()).To(Equal("agent starling clean"))
		Expect(c.Agent.Starling.Clean.ConfigPath).To(Equal("/etc/flock/a.yml"))
	})

	It("has no agent command outside of an agent kind", func() {
		_, _, err := parse("agent", "start")
		Expect(err).To(HaveOccurred())
	})

	It("parses the f5 agent commands", func() {
		c, ctx, err := parse("agent", "f5", "start", "-c", "/etc/flock/f5.yml")
		Expect(err).ToNot(HaveOccurred())
		Expect(ctx.Command()).To(Equal("agent f5 start"))
		Expect(c.Agent.F5.Start.ConfigPath).To(Equal("/etc/flock/f5.yml"))

		c, ctx, err = parse("agent", "f5", "check-config", "-c", "/etc/flock/f5.yml")
		Expect(err).ToNot(HaveOccurred())
		Expect(ctx.Command()).To(Equal("agent f5 check-config"))
		Expect(c.Agent.F5.CheckConfig.ConfigPath).To(Equal("/etc/flock/f5.yml"))
	})

	It("parses the provisioner commands", func() {
		c, ctx, err := parse("provisioner", "start", "-c", "/etc/flock/p.yml")
		Expect(err).ToNot(HaveOccurred())
		Expect(ctx.Command()).To(Equal("provisioner start"))
		Expect(c.Provisioner.Start.ConfigPath).To(Equal("/etc/flock/p.yml"))

		c, ctx, err = parse("provisioner", "check-config", "-c", "/etc/flock/p.yml")
		Expect(err).ToNot(HaveOccurred())
		Expect(ctx.Command()).To(Equal("provisioner check-config"))
		Expect(c.Provisioner.CheckConfig.ConfigPath).To(Equal("/etc/flock/p.yml"))
	})

	It("reads the boolean variables of login", func() {
		GinkgoT().Setenv("FLOCK_SKIP_SSL_VALIDATION", "1")
		GinkgoT().Setenv("FLOCK_HTTP1", "false")

		c, _, err := parse("login")

		Expect(err).ToNot(HaveOccurred())
		Expect(c.Login.SkipSslValidation).To(BeTrue())
		Expect(c.Login.Http1).To(BeFalse())
	})

	It("defaults config path", func() {
		c, _, err := parse("agent", "starling", "start")
		Expect(err).ToNot(HaveOccurred())
		// type:"path" turns the default into an absolute path
		Expect(c.Agent.Starling.Start.ConfigPath).To(HaveSuffix("/config.yml"))
	})

	It("parses the flags of dev", func() {
		c, _, err := parse("dev", "--data-dir", "/tmp/x", "--listen-host", "0.0.0.0", "--hostname", "a",
			"--hostname", "b", "--privileged", "--yes", "--envoy-path", "/usr/local/bin/envoy")
		Expect(err).ToNot(HaveOccurred())
		Expect(c.Dev.DataDir).To(Equal("/tmp/x"))
		Expect(c.Dev.ListenHost).To(Equal("0.0.0.0"))
		Expect(c.Dev.Hostnames).To(Equal([]string{"a", "b"}))
		Expect(c.Dev.Privileged).To(BeTrue())
		Expect(c.Dev.Yes).To(BeTrue())
		Expect(c.Dev.EnvoyPath).To(Equal("/usr/local/bin/envoy"))
	})

	It("defaults the flags of dev to a loopback session in ~/.flock/dev", func() {
		c, _, err := parse("dev")
		Expect(err).ToNot(HaveOccurred())
		Expect(c.Dev.DataDir).To(Equal("~/.flock/dev"))
		Expect(c.Dev.ListenHost).To(Equal("127.0.0.1"))
		Expect(c.Dev.Hostnames).To(BeEmpty())
		Expect(c.Dev.Privileged).To(BeFalse())
	})

	It("reads the hostnames of dev from the environment", func() {
		GinkgoT().Setenv("FLOCK_DEV_HOSTNAMES", "a,b")
		c, _, err := parse("dev")
		Expect(err).ToNot(HaveOccurred())
		Expect(c.Dev.Hostnames).To(Equal([]string{"a", "b"}))
	})

	It("rejects unknown commands", func() {
		for _, args := range [][]string{{}, {"agent"}, {"controller"}} {
			_, _, err := parse(args...)
			Expect(err).To(HaveOccurred(), "%v", args)
		}
	})

	It("prints the flock version line", func() {
		version, commit, date = "1.2.3", "abcdef0123", "2026-10-19"
		Expect(versionLine()).To(Equal("flock 1.2.3, commit abcdef0123, built at 2026-10-19"))
	})

	Describe("cli commands at the root", func() {
		It("parses CLI commands at the root", func() {
			for _, args := range [][]string{{"vss"}, {"create-vs", "-f", "x.yml"}, {"login"}, {"overview"}} {
				_, ctx, err := parse(args...)
				Expect(err).ToNot(HaveOccurred(), "%v", args)
				Expect(isServerCommand(ctx)).To(BeFalse(), "%v", args)
			}
		})

		It("keeps the server commands", func() {
			for _, args := range [][]string{{"controller", "serve", "-c", "a.yml"}, {"agent", "starling", "start"}, {"agent", "starling", "clean"}, {"agent", "f5", "start"}, {"provisioner", "start"}, {"version"}} {
				_, ctx, err := parse(args...)
				Expect(err).ToNot(HaveOccurred(), "%v", args)
				Expect(isServerCommand(ctx)).To(BeTrue(), "%v", args)
			}
		})

		It("has no --version flag", func() {
			_, _, err := parse("--version")

			Expect(err).To(HaveOccurred())
		})

		It("separates help sections", func() {
			help := &bytes.Buffer{}
			parse := func() {
				c := &CLI{}
				parser, err := newParser(c, kong.Exit(func(int) {}), kong.Writers(help, help))
				Expect(err).ToNot(HaveOccurred())
				_, _ = parser.Parse([]string{"--help"})
			}
			parse()

			Expect(help.String()).To(MatchRegexp(`(?s)\nResources\n.*\n  agents \(a\).*\nServer\n.*\n  agent starling start`))
		})

		It("orders the help sections", func() {
			help := &bytes.Buffer{}
			c := &CLI{}
			parser, err := newParser(c, kong.Exit(func(int) {}), kong.Writers(help, help))
			Expect(err).ToNot(HaveOccurred())
			_, _ = parser.Parse([]string{"--help"})

			previousIndex := -1
			for _, title := range []string{"Resources", "Permissions", "Status & logs", "Auth & session", "Tools", "Server"} {
				index := strings.Index(help.String(), "\n"+title+"\n")
				Expect(index).To(BeNumerically(">", previousIndex), title)
				previousIndex = index
			}
		})

		It("reads the cli config path from FLOCK_CLI_CONFIG", func() {
			GinkgoT().Setenv("FLOCK_CLI_CONFIG", "/tmp/from-env.json")

			c, _, err := parse("vss")

			Expect(err).ToNot(HaveOccurred())
			Expect(c.CliConfig).To(Equal("/tmp/from-env.json"))
		})
	})

	Describe("errors", func() {
		BeforeEach(func() {
			previous := cli.CheckForUpdate
			cli.CheckForUpdate = func(context.Context) {}
			DeferCleanup(func() { cli.CheckForUpdate = previous })
		})

		It("prints a short usage and the error on an unknown command", func() {
			output, exitCode := executeArgs("read-vs", "x")

			Expect(output).To(ContainSubstring("flock: error: unexpected argument read-vs"))
			Expect(output).To(ContainSubstring("Usage: flock <command>"))
			Expect(output).ToNot(ContainSubstring("\nServer\n"))
			Expect(exitCode).To(Equal(80))
		})

		It("prints a failing cli command error with the messages format and exits 1", func() {
			missingConfig := filepath.Join(GinkgoT().TempDir(), "config.json")

			output, exitCode := executeArgs("--cli-config", missingConfig, "vs", "x")

			Expect(output).To(Equal("Error config file not found, please login first\n"))
			Expect(exitCode).To(Equal(1))
		})

		It("keeps the kong format for server commands", func() {
			missingConfig := filepath.Join(GinkgoT().TempDir(), "config.yml")

			output, exitCode := executeArgs("controller", "serve", "-c", missingConfig)

			Expect(output).To(HavePrefix("flock: error: read config:"))
			Expect(exitCode).To(Equal(1))
		})
	})

	Describe("check-config", func() {
		writeConfig := func(content string) string {
			path := filepath.Join(GinkgoT().TempDir(), "config.yml")
			Expect(os.WriteFile(path, []byte(content), 0600)).To(Succeed())
			return path
		}
		controllerConfigWithLogin := func(login string, extra string) string {
			certFile := writeConfig("cert")
			store := "store:\n  sql: {type: sqlite, dsn: flock.db, secret_key: s}\n"
			return fmt.Sprintf("mtls:\n  cert_file: %s\n  key_file: %s\nserver:\n  tls:\n    cert_file: cert\n    key_file: key\n%s%s%s", certFile, certFile, login, store, extra)
		}
		controllerConfig := func(extra string) string {
			login := "  login: {type: basic, basic_auth_options: [{username: u, password: p}]}\n" +
				"  jwt_maker: {alg: HS256, private_key: secret}\n"
			return controllerConfigWithLogin(login, extra)
		}

		It("accepts a valid controller config", func() {
			path := writeConfig(controllerConfig(""))

			output, exitCode := executeArgs("controller", "check-config", "-c", path)

			Expect(output).To(Equal(fmt.Sprintf("config %s is valid\n", path)))
			Expect(exitCode).To(Equal(-1))
		})

		It("refuses a controller config without login", func() {
			path := writeConfig(controllerConfigWithLogin("", ""))

			output, exitCode := executeArgs("controller", "check-config", "-c", path)

			Expect(output).To(ContainSubstring("login is required"))
			Expect(exitCode).To(Equal(1))
		})

		It("refuses a login without the options of its type", func() {
			path := writeConfig(controllerConfigWithLogin("  login: {type: ldap}\n", ""))

			output, exitCode := executeArgs("controller", "check-config", "-c", path)

			Expect(output).To(ContainSubstring("server login: ldap_options is required"))
			Expect(exitCode).To(Equal(1))
		})

		It("refuses a jwt_maker key that does not parse", func() {
			login := "  login: {type: basic, basic_auth_options: [{username: u, password: p}]}\n" +
				"  jwt_maker: {alg: RS256, private_key: not-a-pem}\n"
			path := writeConfig(controllerConfigWithLogin(login, ""))

			output, exitCode := executeArgs("controller", "check-config", "-c", path)

			Expect(output).To(ContainSubstring("jwt_maker private key for RS256"))
			Expect(exitCode).To(Equal(1))
		})

		It("refuses an unknown key", func() {
			path := writeConfig(controllerConfig("sever: {}\n"))

			output, exitCode := executeArgs("controller", "check-config", "-c", path)

			Expect(output).To(ContainSubstring("field sever not found"))
			Expect(exitCode).To(Equal(1))
		})

		It("reports an invalid agent config", func() {
			output, exitCode := executeArgs("agent", "starling", "check-config", "-c", writeConfig("log:\n  level: debug\n"))

			Expect(output).To(HavePrefix("flock: error: invalid config"))
			Expect(exitCode).To(Equal(1))
		})
	})

	Describe("admin-token", func() {
		var configPath string

		BeforeEach(func() {
			certFile := filepath.Join(GinkgoT().TempDir(), "cert")
			Expect(os.WriteFile(certFile, []byte("cert"), 0600)).To(Succeed())
			content := fmt.Sprintf("mtls:\n  cert_file: %s\n  key_file: %s\nserver:\n  tls:\n    cert_file: cert\n    key_file: key\n"+
				"  login: {type: basic, basic_auth_options: [{username: u, password: p}]}\n"+
				"  jwt_maker: {alg: HS256, private_key: secret}\n"+
				"store:\n  sql: {type: sqlite, dsn: flock.db, secret_key: s}\n", certFile, certFile)
			configPath = filepath.Join(GinkgoT().TempDir(), "config.yml")
			Expect(os.WriteFile(configPath, []byte(content), 0600)).To(Succeed())
		})

		It("prints a token signed with jwt_maker and says when it expires", func() {
			output, exitCode := executeArgs("controller", "admin-token", "-c", configPath, "--reason", "login down")

			Expect(exitCode).To(Equal(-1))
			lines := strings.Split(strings.TrimSpace(output), "\n")
			Expect(lines).To(HaveLen(2))
			Expect(lines[1]).To(MatchRegexp(`^break-glass token valid until \S+, every call made with it is logged$`))
			cnf, err := controllerconfig.ReadConfigStrict(configPath)
			Expect(err).ToNot(HaveOccurred())
			manager, err := svclogin.NewJwtManager(cnf.Server.JwtMaker)
			Expect(err).ToNot(HaveOccurred())
			username, _, err := manager.VerifyToken(lines[0])
			Expect(err).ToNot(HaveOccurred())
			Expect(username).To(Equal(svclogin.BreakGlassUsername))
			claims, err := manager.BreakGlassClaims(lines[0])
			Expect(err).ToNot(HaveOccurred())
			Expect(claims.Reason).To(Equal("login down"))
		})

		It("requires a reason", func() {
			_, _, err := parse("controller", "admin-token", "-c", configPath)

			Expect(err).To(MatchError(ContainSubstring("--reason")))
		})

		It("refuses a blank reason", func() {
			output, exitCode := executeArgs("controller", "admin-token", "-c", configPath, "--reason", "  \t ")

			Expect(output).To(ContainSubstring("--reason must not be blank"))
			Expect(exitCode).To(Equal(1))
		})

		It("trims the reason", func() {
			output, exitCode := executeArgs("controller", "admin-token", "-c", configPath, "--reason", "  login down \n")

			Expect(exitCode).To(Equal(-1))
			token, _, _ := strings.Cut(strings.TrimSpace(output), "\n")
			cnf, err := controllerconfig.ReadConfigStrict(configPath)
			Expect(err).ToNot(HaveOccurred())
			manager, err := svclogin.NewJwtManager(cnf.Server.JwtMaker)
			Expect(err).ToNot(HaveOccurred())
			claims, err := manager.BreakGlassClaims(token)
			Expect(err).ToNot(HaveOccurred())
			Expect(claims.Reason).To(Equal("login down"))
		})

		It("refuses a ttl above one hour", func() {
			output, exitCode := executeArgs("controller", "admin-token", "-c", configPath, "--reason", "r", "--ttl", "2h")

			Expect(output).To(ContainSubstring("at most 1h"))
			Expect(exitCode).To(Equal(1))
		})
	})

	Describe("update check", func() {
		var checks int

		BeforeEach(func() {
			checks = 0
			previous := cli.CheckForUpdate
			cli.CheckForUpdate = func(context.Context) { checks++ }
			DeferCleanup(func() { cli.CheckForUpdate = previous })
		})

		runCommand := func(args ...string) {
			GinkgoHelper()
			c, ctx, err := parse(args...)
			Expect(err).ToNot(HaveOccurred())
			Expect(run(context.Background(), ctx, c)).To(Succeed())
		}

		It("does not check for updates for server commands", func() {
			runCommand("version")

			Expect(checks).To(Equal(0))
		})

		It("checks for updates for cli commands", func() {
			// clear-session needs no controller, it only removes files next to the given config
			runCommand("--cli-config", filepath.Join(GinkgoT().TempDir(), "config.json"), "clear-session")

			Expect(checks).To(Equal(1))
		})
	})
})
