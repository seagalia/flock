package config

import (
	"fmt"
	"github.com/nats-io/nats.go"
	"github.com/seagalia/flock/sdk/config"
	"github.com/seagalia/flock/sdk/logging"
	"time"
)

type Multi struct {
	Nats *NatsMultiOptions `yaml:"nats"`
}

type NatsMultiOptions struct {

	// Url is a NATS server url, the first one of Servers when both are set
	Url string `yaml:"url"`

	// Servers is the pool of servers to connect to
	Servers []string `yaml:"servers"`

	// NoRandomize keeps the order of the server pool
	NoRandomize bool `yaml:"no_randomize"`

	// NoEcho stops the server from sending back the messages of this connection (server >= 1.2)
	NoEcho bool `yaml:"no_echo"`

	// Name identifies the client to the server
	Name string `yaml:"name"`

	// Verbose makes the server ack every command
	Verbose bool `yaml:"verbose"`

	// Pedantic makes the server validate subjects further
	Pedantic bool `yaml:"pedantic"`

	// Secure enables TLS without server verification by default. NOT RECOMMENDED.
	Secure bool `yaml:"secure"`

	// MaxReconnect is the number of reconnect attempts, negative never gives up. Defaults to 60.
	MaxReconnect *int `yaml:"max_reconnect"`

	// ReconnectWait is the delay before reconnecting to a known server. Defaults to 2s.
	ReconnectWait config.Duration `yaml:"reconnect_wait"`

	// ReconnectJitter is the max random delay added to ReconnectWait without TLS. Defaults to 100ms.
	ReconnectJitter config.Duration `yaml:"reconnect_jitter"`

	// ReconnectJitterTLS is the max random delay added to ReconnectWait with TLS. Defaults to 1s.
	ReconnectJitterTLS config.Duration `yaml:"reconnect_jitter_tls"`

	// Timeout is the dial timeout. Defaults to 2s.
	Timeout config.Duration `yaml:"timeout"`

	// DrainTimeout is the time a drain has to complete. Defaults to 30s.
	DrainTimeout config.Duration `yaml:"drain_timeout"`

	// FlusherTimeout is the max wait for writes to the connection to complete
	FlusherTimeout config.Duration `yaml:"flusher_timeout"`

	// PingInterval is the period of pings to the server, 0 or negative disables them. Defaults to 2m.
	PingInterval config.Duration `yaml:"ping_interval"`

	// MaxPingsOut is the number of unanswered pings before the connection is stale. Defaults to 2.
	MaxPingsOut int `yaml:"max_pings_out"`

	// ReconnectBufSize buffers publishes while reconnecting, publishing fails once full. Defaults to 8MB.
	ReconnectBufSize int `yaml:"reconnect_buf_size"`

	// SubChanLen is the buffer of sync subscriptions, not of async ones. Defaults to 65536.
	SubChanLen int `yaml:"sub_chan_len"`

	// Nkey is the public nkey to authenticate with, UserJWT takes precedence over it
	Nkey string `yaml:"nkey"`

	User string `yaml:"user"`

	Password string `yaml:"password"`

	Token string `yaml:"token"`

	// RetryOnFailedConnect keeps reconnecting (MaxReconnect, ReconnectWait) when the first connection fails
	RetryOnFailedConnect bool `yaml:"retry_on_failed_connect"`

	// Compression asks for websocket compression
	Compression bool `yaml:"compression"`

	// ProxyPath is added to websocket urls, for NATS behind a proxy
	ProxyPath string `yaml:"proxy_path"`

	// InboxPrefix allows the default _INBOX prefix to be customized
	InboxPrefix string `yaml:"inbox_prefix"`

	// IgnoreAuthErrorAbort keeps reconnecting when the server returns the same auth error twice
	IgnoreAuthErrorAbort bool `yaml:"ignore_auth_error_abort"`

	// SkipHostLookup skips the DNS lookup for the server hostname.
	SkipHostLookup bool `yaml:"skip_host_lookup"`
}

func (u *NatsMultiOptions) ToOptions(opts *nats.Options) error {
	entry := logging.Component("nats").With().Str("name", u.Name).Str("url", u.Url).Logger()
	opts.ClosedCB = func(conn *nats.Conn) {
		entry.Info().Msg("connection closed")
	}
	opts.DisconnectedErrCB = func(conn *nats.Conn, err error) {
		if err == nil {
			entry.Info().Msg("connection disconnected")
			return
		}
		entry.Warn().Err(err).Msg("connection disconnected")
	}
	opts.ConnectedCB = func(conn *nats.Conn) {
		entry.Info().Msg("connected")
	}
	opts.ReconnectedCB = func(conn *nats.Conn) {
		entry.Info().Msg("reconnected")
	}
	opts.DiscoveredServersCB = func(conn *nats.Conn) {
		entry.Info().Msg("discovered servers")
	}
	opts.AsyncErrorCB = func(conn *nats.Conn, sub *nats.Subscription, err error) {
		entry.Warn().Str("subject_name", sub.Subject).Err(err).Msg("async error")
	}

	opts.Url = u.Url
	opts.Servers = u.Servers
	opts.NoRandomize = u.NoRandomize
	opts.NoEcho = u.NoEcho
	opts.Name = u.Name
	opts.Verbose = u.Verbose
	opts.Pedantic = u.Pedantic
	opts.Secure = u.Secure
	opts.MaxReconnect = *u.MaxReconnect
	opts.ReconnectWait = time.Duration(u.ReconnectWait)
	opts.ReconnectJitter = time.Duration(u.ReconnectJitter)
	opts.ReconnectJitterTLS = time.Duration(u.ReconnectJitterTLS)
	opts.Timeout = time.Duration(u.Timeout)
	opts.DrainTimeout = time.Duration(u.DrainTimeout)
	opts.FlusherTimeout = time.Duration(u.FlusherTimeout)
	opts.PingInterval = time.Duration(u.PingInterval)
	opts.MaxPingsOut = u.MaxPingsOut
	opts.ReconnectBufSize = u.ReconnectBufSize
	opts.SubChanLen = u.SubChanLen
	opts.Nkey = u.Nkey
	opts.User = u.User
	opts.Password = u.Password
	opts.Token = u.Token
	opts.RetryOnFailedConnect = u.RetryOnFailedConnect
	opts.Compression = u.Compression
	opts.ProxyPath = u.ProxyPath
	opts.InboxPrefix = u.InboxPrefix
	opts.IgnoreAuthErrorAbort = u.IgnoreAuthErrorAbort
	opts.SkipHostLookup = u.SkipHostLookup
	return nil
}

func (u *NatsMultiOptions) UnmarshalYAML(unmarshal func(any) error) error {
	type plain NatsMultiOptions
	if err := unmarshal((*plain)(u)); err != nil {
		return err
	}
	if u.Url == "" && len(u.Servers) == 0 {
		return fmt.Errorf("url or servers must be set")
	}
	if u.MaxReconnect == nil {
		maxReco := 60
		u.MaxReconnect = &maxReco
	}
	if u.ReconnectWait <= 0 {
		u.ReconnectWait = config.Duration(2 * time.Second)
	}
	if u.ReconnectJitter <= 0 {
		u.ReconnectJitter = config.Duration(100 * time.Millisecond)
	}
	if u.ReconnectJitterTLS <= 0 {
		u.ReconnectJitterTLS = config.Duration(1 * time.Second)
	}
	if u.Timeout <= 0 {
		u.Timeout = config.Duration(2 * time.Second)
	}
	if u.DrainTimeout <= 0 {
		u.DrainTimeout = config.Duration(30 * time.Second)
	}
	if u.FlusherTimeout <= 0 {
		u.FlusherTimeout = config.Duration(2 * time.Second)
	}
	if u.PingInterval <= 0 {
		u.PingInterval = config.Duration(2 * time.Minute)
	}
	if u.MaxPingsOut <= 0 {
		u.MaxPingsOut = 2
	}
	if u.ReconnectBufSize <= 0 {
		u.ReconnectBufSize = 8 * 1024 * 1024
	}
	if u.SubChanLen <= 0 {
		u.SubChanLen = 65536
	}
	return nil
}
