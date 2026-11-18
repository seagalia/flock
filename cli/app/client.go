package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/cli/common"
	svclogin "github.com/seagalia/flock/sdk/flock/services/login/v1"
	"github.com/seagalia/flock/sdk/flock/services/login/v1/svcloginconnect"
	"golang.org/x/oauth2"
	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	defaultUnaryTimeout        = 3 * time.Minute
	defaultTLSHandshakeTimeout = 10 * time.Second
	http2ReadIdleTimeout       = 30 * time.Second
	http2PingTimeout           = 15 * time.Second
)

// unaryTimeout bounds the unary calls in both protocols, a variable so specs can shorten it.
var unaryTimeout = defaultUnaryTimeout

// tlsHandshakeTimeout bounds the TLS handshake with the controller, a variable so specs can shorten it. The transport
// is built by hand, so it does not inherit the one of http.DefaultTransport.
var tlsHandshakeTimeout = defaultTLSHandshakeTimeout

type Config struct {
	Host         string `json:"host"`
	Username     string `json:"username"`
	Password     string `json:"-"`
	ClientId     string `json:"-"`
	ClientSecret string `json:"-"`
	// Token is a bearer sent as is, the session file is then neither read nor written
	Token          string  `json:"-"`
	SkipVerify     bool    `json:"skip_verify"`
	Http1Transport bool    `json:"http1_transport"`
	Http1Scheme    string  `json:"http1_scheme"`
	AsUser         *AsUser `json:"as_user"`
}

type AsUser struct {
	Username string
	Groups   []string
}

// NewClient makes every client of the flock controller, tests replace it to talk to a fake controller.
var NewClient = makeClient

func makeClient(cnf *Config, interceptors ...connect.ClientInterceptor) (*connect.Client, error) {
	transport, baseURL, err := ControllerTransport(cnf)
	if err != nil {
		return nil, err
	}
	// no timeout on the http client: it would cut log streams
	httpClient := &http.Client{Transport: transport}
	interceptors = append([]connect.ClientInterceptor{unaryTimeoutInterceptor(unaryTimeout)}, interceptors...)
	if !cnf.Http1Transport {
		return connect.NewClient(connecthttp.NewTransport(httpClient, baseURL, connecthttp.WithGRPC()), interceptors...), nil
	}
	// HTTP/1 mode is for proxies that only speak HTTP/1.1, the Connect protocol works over it
	return connect.NewClient(connecthttp.NewTransport(httpClient, baseURL), interceptors...), nil
}

// ControllerTransport gives the transport and the base url to reach the controller of cnf. It is HTTP/2 over TLS by
// default, and HTTP/1.1 with the scheme of http1_scheme in HTTP/1 mode. The connect client and the proxy of
// `flock dashboard` share it, so both reach the controller the same way.
func ControllerTransport(cnf *Config) (transport *http.Transport, baseURL string, err error) {
	transport = &http.Transport{
		Proxy:       http.ProxyFromEnvironment,
		DialContext: http.DefaultTransport.(*http.Transport).DialContext,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: cnf.SkipVerify,
		},
		TLSHandshakeTimeout: tlsHandshakeTimeout,
		// pings detect a dead connection under a stream, which has no timeout of its own
		HTTP2: &http.HTTP2Config{
			SendPingTimeout: http2ReadIdleTimeout,
			PingTimeout:     http2PingTimeout,
		},
		Protocols: new(http.Protocols),
	}
	if !cnf.Http1Transport {
		transport.Protocols.SetHTTP2(true)
		return transport, "https://" + cnf.Host, nil
	}

	http1Scheme := cnf.Http1Scheme
	if http1Scheme == "" {
		http1Scheme = "https"
	}
	if http1Scheme != "https" && http1Scheme != "http" {
		return nil, "", fmt.Errorf("invalid http1 scheme: %s", http1Scheme)
	}
	transport.Protocols.SetHTTP1(true)
	return transport, http1Scheme + "://" + cnf.Host, nil
}

// unaryTimeoutInterceptor bounds the unary calls only, because a timeout on the http client would also bound reading
// the body and cut log streams.
func unaryTimeoutInterceptor(timeout time.Duration) connect.ClientInterceptor {
	return func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			if spec.StreamType != connect.StreamTypeUnary {
				return next(ctx, spec)
			}
			ctx, cancel := context.WithTimeout(ctx, timeout)
			stream, err := next(ctx, spec)
			if err != nil {
				cancel()
				return nil, err
			}
			return &cancelOnClose{ClientStream: stream, cancel: cancel}, nil
		}
	}
}

// cancelOnClose releases the timeout of a unary call once the call is done, connect closes
// every unary stream.
type cancelOnClose struct {
	connect.ClientStream
	cancel context.CancelFunc
}

func (s *cancelOnClose) Close() error {
	defer s.cancel()
	return s.ClientStream.Close()
}

func MakeClient(ctx context.Context, cnf *Config, noAuth bool) (*connect.Client, oauth2.TokenSource, error) {
	if noAuth {
		client, err := NewClient(cnf)
		if err != nil {
			return nil, nil, fmt.Errorf("Error when creating grpc connection: %s", err)
		}
		return client, nil, nil
	}
	tokenSource, err := SessionTokenSource(ctx, cnf)
	if err != nil {
		return nil, nil, err
	}
	_, err = tokenSource.Token()
	if err != nil {
		return nil, nil, common.WrapError("Error when checking credentials", err)
	}
	interceptors := []connect.ClientInterceptor{svclogin.TokenInterceptor(tokenSource)}
	if cnf.AsUser != nil {
		interceptors = append(interceptors, AsUserInterceptor(cnf.AsUser.Username, cnf.AsUser.Groups))
		msg.UseStderr()
		msg.Warningf("You are acting as %s. You can clear with %s\n",
			msg.Cyan(cnf.AsUser.Username).String(),
			msg.Green("flock sau --clear").String(),
		)
		msg.UseStdout()
	}
	client, err := NewClient(cnf, interceptors...)
	if err != nil {
		return nil, nil, common.WrapError("Error when creating grpc connection", err)
	}
	return client, tokenSource, nil
}

// SessionTokenSource gives the bearer of the cli session of cnf, FLOCK_TOKEN first. It asks nothing of the controller
// before its first Token call.
func SessionTokenSource(ctx context.Context, cnf *Config) (oauth2.TokenSource, error) {
	path, err := sessionPath(cnf.Host)
	if err != nil {
		return nil, err
	}
	return newTokenSource(ctx, cnf, path)
}

// CompletionClient makes the client of shell completion for the config at path. It sends the stored token as is and never
// renews it. The caller bounds each call with the deadline of its ctx, so a stalled controller cannot hang the user's
// shell.
func CompletionClient(path string) (*connect.Client, error) {
	cnf, err := RetrieveConfig(path)
	if err != nil {
		return nil, err
	}
	var tokens oauth2.TokenSource = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: cnf.Token})
	if cnf.Token == "" {
		sessionFile, err := sessionPath(cnf.Host)
		if err != nil {
			return nil, err
		}
		tokens = storedOnlyTokenSource{path: sessionFile}
	}
	return NewClient(cnf, svclogin.TokenInterceptor(tokens))
}

func createConfFromEnv() (*Config, error) {
	cnf := &Config{}
	if host, ok := os.LookupEnv("FLOCK_HOST"); ok {
		cnf.Host = host
	}
	if username, ok := os.LookupEnv("FLOCK_USERNAME"); ok {
		cnf.Username = username
	}
	if password, ok := os.LookupEnv("FLOCK_PASSWORD"); ok {
		cnf.Password = password
	}
	cnf.ClientId = os.Getenv("FLOCK_CLIENT_ID")
	cnf.ClientSecret = os.Getenv("FLOCK_CLIENT_SECRET")
	cnf.Token = os.Getenv("FLOCK_TOKEN")
	if scheme, ok := os.LookupEnv("FLOCK_HTTP1_SCHEME"); ok {
		cnf.Http1Scheme = scheme
	}
	err := boolsFromEnv(cnf)
	if err != nil {
		return nil, err
	}
	return cnf, nil
}

// boolsFromEnv sets the boolean settings given in the environment, a false value wins over the config file too. They
// are parsed like kong parses the flags of login bound to the same variables, so a value means the same thing for
// every command.
func boolsFromEnv(cnf *Config) error {
	err := boolFromEnv("FLOCK_HTTP1", &cnf.Http1Transport)
	if err != nil {
		return err
	}
	return boolFromEnv("FLOCK_SKIP_SSL_VALIDATION", &cnf.SkipVerify)
}

func boolFromEnv(name string, target *bool) error {
	val, isSet := os.LookupEnv(name)
	if !isSet {
		return nil
	}
	switch strings.ToLower(val) {
	case "true", "1", "yes":
		*target = true
	case "false", "0", "no":
		*target = false
	default:
		return fmt.Errorf("%s must be true, 1, yes, false, 0 or no, got %q", name, val)
	}
	return nil
}

func loginPath(dir, target string) string {
	username := "default"
	currentUser, err := user.Current()
	if err == nil {
		username = currentUser.Username
	}
	target = strings.Replace(target, ":", "_", -1)
	target = strings.Replace(target, ".", "_", -1)
	username = strings.Replace(username, ":", "_", -1)
	username = strings.Replace(username, ".", "_", -1)
	return filepath.Join(dir, fmt.Sprintf("flock.%s.%s.token", target, username))
}

// sessionPath gives the session file of host. Sessions live in the user's own cache dir because an OIDC session holds a
// refresh token that other local users must not read.
func sessionPath(host string) (string, error) {
	dir, err := os.UserCacheDir()
	if err == nil {
		return loginPath(filepath.Join(dir, "flock", "sessions"), host), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no cache nor home directory to keep the session in: %w", err)
	}
	return loginPath(filepath.Join(home, ".flock", "sessions"), host), nil
}

func ClearLogin(path string, cnf *Config) {
	MergeConfig(path, cnf)
	removeSession(cnf.Host)
}

func RetrieveConfig(path string) (*Config, error) {
	confEnv, err := createConfFromEnv()
	if err != nil {
		return nil, err
	}
	confRaw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if confEnv.Host != "" {
				return confEnv, nil
			}
			return nil, fmt.Errorf("config file not found, please login first")
		}
		return nil, err
	}
	config := &Config{}
	err = json.Unmarshal(confRaw, config)
	if err != nil {
		return nil, err
	}
	err = boolsFromEnv(config)
	if err != nil {
		return nil, err
	}
	if confEnv.Http1Scheme != "" {
		config.Http1Scheme = confEnv.Http1Scheme
	}
	if confEnv.Host != "" {
		config.Host = confEnv.Host
	}
	if confEnv.Username != "" {
		config.Username = confEnv.Username
	}
	if confEnv.Password != "" {
		config.Password = confEnv.Password
	}
	config.ClientId = confEnv.ClientId
	config.ClientSecret = confEnv.ClientSecret
	config.Token = confEnv.Token
	return config, nil
}

// GetServiceAuth gives the login service of the controller of cnf
func GetServiceAuth(ctx context.Context, cnf *Config) (*svclogin.ServiceAuth, error) {
	client, _, err := MakeClient(ctx, cnf, true)
	if err != nil {
		return nil, err
	}
	return svcloginconnect.NewLoginClient(client).GetServiceAuth(ctx, &emptypb.Empty{})
}

func GetCurrentUsername(path string) string {
	config, err := RetrieveConfig(path)
	if err != nil {
		me, err := user.Current()
		if err != nil {
			return ""
		}
		return me.Username
	}
	return config.Username
}

func CreateClientFromFile(ctx context.Context, path string, noAuth bool) (*connect.Client, oauth2.TokenSource, *Config, error) {
	config, err := RetrieveConfig(path)
	if err != nil {
		return nil, nil, nil, err
	}
	client, tokenSource, err := MakeClient(ctx, config, noAuth)
	if err != nil {
		return nil, nil, nil, err
	}
	return client, tokenSource, config, nil
}

func MergeConfig(path string, cnf *Config) {
	config, err := RetrieveConfig(path)
	if err != nil {
		return
	}
	if cnf.Host == "" {
		cnf.Host = config.Host
		cnf.SkipVerify = config.SkipVerify
	}
}

func CreateClient(ctx context.Context, path string, cnf *Config, noAuth bool) (*connect.Client, oauth2.TokenSource, error) {
	MergeConfig(path, cnf)
	err := os.MkdirAll(filepath.Dir(path), 0755)
	if err != nil {
		return nil, nil, err
	}
	client, tokenSource, err := MakeClient(ctx, cnf, noAuth)
	if err != nil {
		return nil, nil, err
	}
	err = WriteConfig(path, cnf)
	if err != nil {
		return nil, nil, err
	}
	return client, tokenSource, nil
}

func WriteConfig(path string, cnf *Config) error {
	b, err := json.MarshalIndent(cnf, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, ".config-*", b)
}

func ClearSession(path string) {
	cnf, err := RetrieveConfig(path)
	if err != nil {
		return
	}
	removeSession(cnf.Host)
}

func removeSession(host string) {
	path, err := sessionPath(host)
	if err != nil {
		return
	}
	os.Remove(path) // nolint:errcheck
}
