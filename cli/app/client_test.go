package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	"github.com/seagalia/flock/sdk/flock/services/lookup/v1/svclookupconnect"
)

var flockEnvVars = []string{
	"FLOCK_HOST",
	"FLOCK_USERNAME",
	"FLOCK_PASSWORD",
	"FLOCK_CLIENT_ID",
	"FLOCK_CLIENT_SECRET",
	"FLOCK_HTTP1",
	"FLOCK_SKIP_SSL_VALIDATION",
	"FLOCK_HTTP1_SCHEME",
	"FLOCK_TOKEN",
}

// clearFlockEnv makes tests independent of the developer's environment
// and restores the original values afterwards.
func clearFlockEnv() {
	for _, name := range flockEnvVars {
		previous, wasSet := os.LookupEnv(name)
		Expect(os.Unsetenv(name)).To(Succeed())
		DeferCleanup(func() {
			if wasSet {
				os.Setenv(name, previous) //nolint:errcheck
				return
			}
			os.Unsetenv(name) //nolint:errcheck
		})
	}
}

// isolateSessions moves the user's cache dir, where the session files live, to a temp dir and gives that cache dir
func isolateSessions() string {
	home := GinkgoT().TempDir()
	GinkgoT().Setenv("HOME", home)
	GinkgoT().Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	cacheDir, err := os.UserCacheDir()
	Expect(err).NotTo(HaveOccurred())
	return cacheDir
}

func setEnv(name, value string) {
	Expect(os.Setenv(name, value)).To(Succeed())
}

func writeConfigFile(cnf *Config) string {
	path := filepath.Join(GinkgoT().TempDir(), "config.json")
	Expect(WriteConfig(path, cnf)).To(Succeed())
	return path
}

var _ = Describe("Config", func() {
	BeforeEach(clearFlockEnv)

	Describe("createConfFromEnv", func() {
		It("reads host, credentials and http1 settings", func() {
			setEnv("FLOCK_HOST", "flock.local:443")
			setEnv("FLOCK_USERNAME", "alice")
			setEnv("FLOCK_PASSWORD", "secret")
			setEnv("FLOCK_HTTP1", "1")
			setEnv("FLOCK_HTTP1_SCHEME", "http")

			cnf, err := createConfFromEnv()
			Expect(err).ToNot(HaveOccurred())

			Expect(cnf.Host).To(Equal("flock.local:443"))
			Expect(cnf.Username).To(Equal("alice"))
			Expect(cnf.Password).To(Equal("secret"))
			Expect(cnf.Http1Transport).To(BeTrue())
			Expect(cnf.Http1Scheme).To(Equal("http"))
		})

		It("reads the bearer token and the client credentials", func() {
			setEnv("FLOCK_TOKEN", "tok")
			setEnv("FLOCK_CLIENT_ID", "machine")
			setEnv("FLOCK_CLIENT_SECRET", "machine-secret")

			cnf, err := createConfFromEnv()
			Expect(err).ToNot(HaveOccurred())

			Expect(cnf.Token).To(Equal("tok"))
			Expect(cnf.ClientId).To(Equal("machine"))
			Expect(cnf.ClientSecret).To(Equal("machine-secret"))
		})

		It("reads FLOCK_SKIP_SSL_VALIDATION into SkipVerify", func() {
			setEnv("FLOCK_SKIP_SSL_VALIDATION", "1")

			cnf, err := createConfFromEnv()
			Expect(err).ToNot(HaveOccurred())

			Expect(cnf.SkipVerify).To(BeTrue())
			Expect(cnf.Http1Transport).To(BeFalse())
		})

		DescribeTable("parses the boolean variables",
			func(value string, expected bool) {
				setEnv("FLOCK_HTTP1", value)
				setEnv("FLOCK_SKIP_SSL_VALIDATION", value)

				cnf, err := createConfFromEnv()

				Expect(err).ToNot(HaveOccurred())
				Expect(cnf.Http1Transport).To(Equal(expected))
				Expect(cnf.SkipVerify).To(Equal(expected))
			},
			Entry("1", "1", true),
			Entry("true", "true", true),
			Entry("0", "0", false),
			Entry("false", "false", false),
			Entry("YES", "YES", true),
			Entry("no", "no", false),
		)

		It("refuses a boolean variable that isn't a boolean", func() {
			setEnv("FLOCK_SKIP_SSL_VALIDATION", "maybe")

			_, err := createConfFromEnv()

			Expect(err).To(MatchError(ContainSubstring("FLOCK_SKIP_SSL_VALIDATION")))
		})

		It("returns an empty config without environment", func() {
			cnf, err := createConfFromEnv()
			Expect(err).ToNot(HaveOccurred())
			Expect(cnf).To(Equal(&Config{}))
		})
	})

	Describe("RetrieveConfig", func() {
		It("fails when there is no file and no host in env", func() {
			missing := filepath.Join(GinkgoT().TempDir(), "missing.json")

			_, err := RetrieveConfig(missing)

			Expect(err).To(MatchError(ContainSubstring("please login first")))
		})

		It("falls back to env config when there is no file", func() {
			setEnv("FLOCK_HOST", "flock.local:443")
			missing := filepath.Join(GinkgoT().TempDir(), "missing.json")

			cnf, err := RetrieveConfig(missing)

			Expect(err).NotTo(HaveOccurred())
			Expect(cnf.Host).To(Equal("flock.local:443"))
		})

		It("reads the config file", func() {
			path := writeConfigFile(&Config{Host: "file.local:443", Username: "bob"})

			cnf, err := RetrieveConfig(path)

			Expect(err).NotTo(HaveOccurred())
			Expect(cnf.Host).To(Equal("file.local:443"))
			Expect(cnf.Username).To(Equal("bob"))
		})

		It("reads a config file written with the login services keys", func() {
			path := filepath.Join(GinkgoT().TempDir(), "config.json")
			Expect(os.WriteFile(path, []byte(`{"host": "file.local:443", "username": "bob", "auth_name": "ldap",
				"auth_uri": "https://auth.local", "auth_flow_type": 1}`), 0600)).To(Succeed())

			cnf, err := RetrieveConfig(path)

			Expect(err).NotTo(HaveOccurred())
			Expect(cnf).To(Equal(&Config{Host: "file.local:443", Username: "bob"}))
		})

		It("lets env values override the config file", func() {
			path := writeConfigFile(&Config{Host: "file.local:443", Username: "bob"})
			setEnv("FLOCK_HOST", "env.local:443")
			setEnv("FLOCK_USERNAME", "alice")
			setEnv("FLOCK_PASSWORD", "secret")
			setEnv("FLOCK_HTTP1", "1")
			setEnv("FLOCK_HTTP1_SCHEME", "http")
			setEnv("FLOCK_TOKEN", "tok")
			setEnv("FLOCK_CLIENT_ID", "machine")
			setEnv("FLOCK_CLIENT_SECRET", "machine-secret")

			cnf, err := RetrieveConfig(path)

			Expect(err).NotTo(HaveOccurred())
			Expect(cnf.Token).To(Equal("tok"))
			Expect(cnf.ClientId).To(Equal("machine"))
			Expect(cnf.ClientSecret).To(Equal("machine-secret"))
			Expect(cnf.Host).To(Equal("env.local:443"))
			Expect(cnf.Username).To(Equal("alice"))
			Expect(cnf.Password).To(Equal("secret"))
			Expect(cnf.Http1Transport).To(BeTrue())
			Expect(cnf.Http1Scheme).To(Equal("http"))
		})

		It("lets a false env value turn off a setting of the config file", func() {
			path := writeConfigFile(&Config{Host: "file.local:443", Http1Transport: true, SkipVerify: true})
			setEnv("FLOCK_HTTP1", "false")
			setEnv("FLOCK_SKIP_SSL_VALIDATION", "0")

			cnf, err := RetrieveConfig(path)

			Expect(err).NotTo(HaveOccurred())
			Expect(cnf.Http1Transport).To(BeFalse())
			Expect(cnf.SkipVerify).To(BeFalse())
		})

		It("fails on an invalid config file", func() {
			path := filepath.Join(GinkgoT().TempDir(), "config.json")
			Expect(os.WriteFile(path, []byte("{not json"), 0600)).To(Succeed())

			_, err := RetrieveConfig(path)

			Expect(err).To(HaveOccurred())
		})
	})

	Describe("WriteConfig", func() {
		It("never writes the password, the bearer token nor the client credentials to disk", func() {
			path := writeConfigFile(&Config{
				Host: "file.local:443", Username: "bob", Password: "secret",
				Token: "tok", ClientId: "machine", ClientSecret: "machine-secret",
			})

			raw, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(raw)).NotTo(ContainSubstring("secret"))
			Expect(string(raw)).NotTo(ContainSubstring("tok"))
			Expect(string(raw)).NotTo(ContainSubstring("machine"))

			fromFile := map[string]any{}
			Expect(json.Unmarshal(raw, &fromFile)).To(Succeed())
			Expect(fromFile).To(HaveKeyWithValue("username", "bob"))
		})
	})

	Describe("MergeConfig", func() {
		var path string

		BeforeEach(func() {
			path = writeConfigFile(&Config{Host: "file.local:443", SkipVerify: true})
		})

		It("fills the host from the saved config", func() {
			cnf := &Config{}

			MergeConfig(path, cnf)

			Expect(cnf.Host).To(Equal("file.local:443"))
			Expect(cnf.SkipVerify).To(BeTrue())
		})

		It("keeps the settings of another host", func() {
			cnf := &Config{Host: "other.local:443"}

			MergeConfig(path, cnf)

			Expect(cnf).To(Equal(&Config{Host: "other.local:443"}))
		})

		It("does nothing when there is no saved config", func() {
			cnf := &Config{Host: "other.local:443"}

			MergeConfig(filepath.Join(GinkgoT().TempDir(), "missing.json"), cnf)

			Expect(cnf).To(Equal(&Config{Host: "other.local:443"}))
		})
	})

	Describe("sessionPath", func() {
		It("keeps the sessions in the user's cache dir", func() {
			cacheDir := isolateSessions()

			path, err := sessionPath("flock.local:443")

			Expect(err).NotTo(HaveOccurred())
			Expect(filepath.Dir(path)).To(Equal(filepath.Join(cacheDir, "flock", "sessions")))
		})
	})

	Describe("loginPath", func() {
		It("builds a token path without dots or colons in host and user", func() {
			username := "default"
			if currentUser, err := user.Current(); err == nil {
				username = currentUser.Username
			}
			Expect(username).NotTo(BeEmpty())

			path := loginPath("/tmp", "flock.local:443")

			Expect(filepath.Dir(path)).To(Equal("/tmp"))
			Expect(filepath.Base(path)).To(HavePrefix("flock.flock_local_443."))
			Expect(filepath.Base(path)).To(HaveSuffix(".token"))
			userPart := filepath.Base(path)
			userPart = userPart[len("flock.flock_local_443.") : len(userPart)-len(".token")]
			Expect(userPart).NotTo(ContainSubstring("."))
			Expect(userPart).NotTo(ContainSubstring(":"))
		})
	})

	Describe("makeClient", func() {
		It("rejects an unknown http1 scheme", func() {
			_, err := makeClient(&Config{Host: "flock.local:443", Http1Transport: true, Http1Scheme: "ftp"})

			Expect(err).To(MatchError("invalid http1 scheme: ftp"))
		})

		It("talks Connect over HTTP/1.1 and streams server messages", func() {
			logs := &streamingLogs{}
			srv := startServer(logs, false)

			client, err := makeClient(&Config{Host: srv.Listener.Addr().String(), Http1Transport: true, SkipVerify: true})
			Expect(err).NotTo(HaveOccurred())
			stream, err := svclookupconnect.NewLookupClient(client).GetLogsStream(context.Background(), &svclookup.GetLogsRequest{})
			Expect(err).NotTo(HaveOccurred())
			defer stream.Close() //nolint:errcheck

			first, err := stream.Receive()
			Expect(err).NotTo(HaveOccurred())
			Expect(first.GetLogStream().GetLogLine()).To(Equal("line 1"))
			Expect(logs.sent.Load()).To(BeNumerically("<", 3), "the first line must arrive before the whole response is written")
			lines := []string{first.GetLogStream().GetLogLine()}
			for {
				resp, err := stream.Receive()
				if errors.Is(err, io.EOF) {
					break
				}
				Expect(err).NotTo(HaveOccurred())
				lines = append(lines, resp.GetLogStream().GetLogLine())
			}
			Expect(lines).To(Equal([]string{"line 1", "line 2", "line 3"}))
			Expect(logs.requestProtoMajor()).To(Equal(1))
		})

		Describe("HTTP/1.1 timeout", func() {
			BeforeEach(func() {
				previous := unaryTimeout
				unaryTimeout = 100 * time.Millisecond
				DeferCleanup(func() { unaryTimeout = previous })
			})

			It("lets a stream outlive the timeout", func() {
				logs := &streamingLogs{}
				srv := startServer(logs, false)
				client, err := makeClient(&Config{Host: srv.Listener.Addr().String(), Http1Transport: true, SkipVerify: true})
				Expect(err).NotTo(HaveOccurred())

				stream, err := svclookupconnect.NewLookupClient(client).GetLogsStream(context.Background(), &svclookup.GetLogsRequest{})
				Expect(err).NotTo(HaveOccurred())
				defer stream.Close() //nolint:errcheck
				received := 0
				for {
					_, err := stream.Receive()
					if errors.Is(err, io.EOF) {
						break
					}
					Expect(err).NotTo(HaveOccurred())
					received++
				}

				Expect(received).To(Equal(3))
			})

			It("fails a unary call that never answers", func() {
				logs := &streamingLogs{hangUnary: true}
				srv := startServer(logs, false)
				client, err := makeClient(&Config{Host: srv.Listener.Addr().String(), Http1Transport: true, SkipVerify: true})
				Expect(err).NotTo(HaveOccurred())

				start := time.Now()
				_, err = svclookupconnect.NewLookupClient(client).ListAgent(context.Background(), &svclookup.ListAgentRequest{})

				Expect(connect.CodeOf(err)).To(Equal(connect.CodeDeadlineExceeded))
				Expect(time.Since(start)).To(BeNumerically("<", 5*time.Second))
			})
		})

		It("fails a unary call in gRPC mode that never answers", func() {
			previous := unaryTimeout
			unaryTimeout = 100 * time.Millisecond
			DeferCleanup(func() { unaryTimeout = previous })
			srv := startServer(&streamingLogs{hangUnary: true}, true)
			client, err := makeClient(&Config{Host: srv.Listener.Addr().String(), SkipVerify: true})
			Expect(err).NotTo(HaveOccurred())

			start := time.Now()
			_, err = svclookupconnect.NewLookupClient(client).ListAgent(context.Background(), &svclookup.ListAgentRequest{})

			Expect(connect.CodeOf(err)).To(Equal(connect.CodeDeadlineExceeded))
			Expect(time.Since(start)).To(BeNumerically("<", 5*time.Second))
		})

		It("gives up on a controller that never completes the TLS handshake", func() {
			previous := tlsHandshakeTimeout
			tlsHandshakeTimeout = 200 * time.Millisecond
			DeferCleanup(func() { tlsHandshakeTimeout = previous })
			silent, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(silent.Close)
			go func() {
				for {
					// the connection is kept open and never read, like a controller stuck before the handshake
					_, err := silent.Accept()
					if err != nil {
						return
					}
				}
			}()
			client, err := makeClient(&Config{Host: silent.Addr().String(), SkipVerify: true})
			Expect(err).NotTo(HaveOccurred())

			start := time.Now()
			_, err = svclookupconnect.NewLookupClient(client).ListAgent(context.Background(), &svclookup.ListAgentRequest{})

			Expect(err).To(HaveOccurred())
			Expect(time.Since(start)).To(BeNumerically("<", 5*time.Second))
		})

		It("uses the gRPC protocol over HTTP/2 by default", func() {
			logs := &streamingLogs{}
			srv := startServer(logs, true)

			client, err := makeClient(&Config{Host: srv.Listener.Addr().String(), SkipVerify: true})
			Expect(err).NotTo(HaveOccurred())
			_, err = svclookupconnect.NewLookupClient(client).ListAgent(context.Background(), &svclookup.ListAgentRequest{})

			Expect(err).NotTo(HaveOccurred())
			Expect(logs.requestProtoMajor()).To(Equal(2))
			Expect(logs.requestContentType()).To(HavePrefix("application/grpc"))
		})
	})
})

// streamingLogs is a lookup service streaming 3 log lines with a pause between them,
// it records the protocol of the last request it received. With hangUnary, its unary
// call answers only when the client gives up.
type streamingLogs struct {
	svclookupconnect.UnimplementedLookupHandler
	hangUnary   bool
	sent        atomic.Int32
	mu          sync.Mutex
	protoMajor  int
	contentType string
}

func (s *streamingLogs) GetLogsStream(_ context.Context, _ *svclookup.GetLogsRequest, stream svclookupconnect.LookupGetLogsStreamServerStream) error {
	for i := 1; i <= 3; i++ {
		if i > 1 {
			time.Sleep(200 * time.Millisecond)
		}
		err := stream.Send(&svclookup.LogsStreamResponse{Event: &svclookup.LogsStreamResponse_LogStream{LogStream: &svclookup.LogsStreamResponse_AgentLogLine{LogLine: fmt.Sprintf("line %d", i)}}})
		if err != nil {
			return err
		}
		s.sent.Add(1)
	}
	return nil
}

func (s *streamingLogs) ListAgent(ctx context.Context, _ *svclookup.ListAgentRequest) (*svclookup.ListAgentResponse, error) {
	if s.hangUnary {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &svclookup.ListAgentResponse{}, nil
}

func (s *streamingLogs) record(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.protoMajor = r.ProtoMajor
	s.contentType = r.Header.Get("Content-Type")
}

func (s *streamingLogs) requestProtoMajor() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.protoMajor
}

func (s *streamingLogs) requestContentType() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.contentType
}

// startServer serves the lookup service over TLS, with HTTP/2 or HTTP/1.1 only.
func startServer(logs *streamingLogs, enableHTTP2 bool) *httptest.Server {
	server := connect.NewServer()
	svclookupconnect.RegisterLookupHandler(server, logs)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs.record(r)
		mux.ServeHTTP(w, r)
	}))
	srv.EnableHTTP2 = enableHTTP2
	srv.StartTLS()
	DeferCleanup(srv.Close)
	return srv
}

var _ = Describe("CompletionClient", func() {
	BeforeEach(clearFlockEnv)

	It("does not renew an expired session", func() {
		isolateSessions()
		accepted := atomic.Int32{}
		silent, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(silent.Close)
		go func() {
			for {
				_, err := silent.Accept()
				if err != nil {
					return
				}
				accepted.Add(1)
			}
		}()
		host := silent.Addr().String()
		sessionFile, err := sessionPath(host)
		Expect(err).NotTo(HaveOccurred())
		Expect(writeStoredToken(sessionFile, &storedToken{Kind: "flock", AccessToken: "old", Expiry: time.Now().Add(-time.Hour)})).To(Succeed())
		path := writeConfigFile(&Config{Host: host, Username: "alice", SkipVerify: true})
		setEnv("FLOCK_USERNAME", "alice")
		setEnv("FLOCK_PASSWORD", "secret")

		client, err := CompletionClient(path)
		Expect(err).NotTo(HaveOccurred())
		_, err = svclookupconnect.NewLookupClient(client).ListAgent(context.Background(), &svclookup.ListAgentRequest{})

		Expect(err).To(MatchError(ContainSubstring(errSessionExpired.Error())))
		Expect(accepted.Load()).To(BeZero())
	})

	It("lets the deadline of the ctx bound a call to a controller that never answers", func() {
		setEnv("FLOCK_TOKEN", "tok")
		srv := startServer(&streamingLogs{hangUnary: true}, true)
		path := writeConfigFile(&Config{Host: srv.Listener.Addr().String(), SkipVerify: true})

		client, err := CompletionClient(path)
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, err = svclookupconnect.NewLookupClient(client).ListAgent(ctx, &svclookup.ListAgentRequest{})

		Expect(connect.CodeOf(err)).To(Equal(connect.CodeDeadlineExceeded))
		Expect(time.Since(start)).To(BeNumerically("<", 3*time.Second))
	})
})

var _ = Describe("atomic writes", func() {
	failRename := func() {
		previous := renameFile
		renameFile = func(string, string) error { return errors.New("rename failed") }
		DeferCleanup(func() { renameFile = previous })
	}

	It("keeps the previous config readable when the move fails", func() {
		clearFlockEnv()
		path := writeConfigFile(&Config{Host: "old.local:443"})
		failRename()

		err := WriteConfig(path, &Config{Host: "new.local:443"})

		Expect(err).To(HaveOccurred())
		kept, err := RetrieveConfig(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(kept.Host).To(Equal("old.local:443"))
		leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".config-*"))
		Expect(err).NotTo(HaveOccurred())
		Expect(leftovers).To(BeEmpty())
	})

	It("writes the config with 0600", func() {
		path := writeConfigFile(&Config{Host: "old.local:443"})

		info, err := os.Stat(path)

		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0600)))
	})
})
