// Package bigip calls the iControl REST and AS3 APIs of BIG-IP devices with net/http. A client knows every device
// of an HA pair and sends to the active one: the devices sync their configuration themselves.
package bigip

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"
)

type Options struct {
	// URLs are the management URLs of the devices, in order of preference.
	URLs          []string
	Username      string
	Password      string
	LoginProvider string
	TLSConfig     *tls.Config
	// RequestTimeout bounds one call, the polling of an AS3 task included.
	RequestTimeout time.Duration
	// PollInterval is the wait between two polls of an AS3 task, 1s when zero.
	PollInterval time.Duration
	// FailoverCacheTTL is how long the active device is remembered, 10s when zero, not at all when negative.
	FailoverCacheTTL time.Duration
}

type Client struct {
	opts Options
	http *http.Client

	mu       sync.Mutex
	tokens   map[string]string
	active   string
	activeAt time.Time
}

func New(o Options) *Client {
	if o.LoginProvider == "" {
		o.LoginProvider = "tmos"
	}
	if o.RequestTimeout == 0 {
		o.RequestTimeout = 2 * time.Minute
	}
	if o.PollInterval == 0 {
		o.PollInterval = time.Second
	}
	if o.FailoverCacheTTL == 0 {
		o.FailoverCacheTTL = 10 * time.Second
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = o.TLSConfig
	return &Client{
		opts:   o,
		http:   &http.Client{Transport: transport, Timeout: o.RequestTimeout},
		tokens: map[string]string{},
	}
}

// device is the URL of the device to configure: the active one, or the first reachable when none says ACTIVE (a
// device alone, or a pair in the middle of a failover). Reaching none is a retryable error.
func (c *Client) device(ctx context.Context) (string, error) {
	c.mu.Lock()
	cached := c.active != "" && c.opts.FailoverCacheTTL > 0 && time.Since(c.activeAt) < c.opts.FailoverCacheTTL
	if cached {
		defer c.mu.Unlock()
		return c.active, nil
	}
	c.mu.Unlock()

	firstReachable := ""
	var lastErr error
	for _, url := range c.opts.URLs {
		status, err := c.failoverStatus(ctx, url)
		if err != nil {
			lastErr = err
			continue
		}
		if status == "ACTIVE" {
			c.remember(url)
			return url, nil
		}
		if firstReachable == "" {
			firstReachable = url
		}
	}
	if firstReachable == "" {
		return "", lastErr
	}
	c.remember(firstReachable)
	return firstReachable, nil
}

func (c *Client) remember(url string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active, c.activeAt = url, time.Now()
}

// forget drops the remembered device after it failed to answer, the next call looks for the active one again.
func (c *Client) forget() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active = ""
}

type response struct {
	status int
	body   []byte
}

// do calls a device with its token, logging in when it has none and once more when the token is refused. An
// answer of 400 or more is an *Error.
func (c *Client) do(ctx context.Context, device, method, path string, body []byte) (response, error) {
	token, err := c.token(ctx, device)
	if err != nil {
		return response{}, err
	}
	resp, err := c.send(ctx, device, method, path, body, token)
	if err == nil && resp.status == http.StatusUnauthorized {
		c.dropToken(device)
		token, err = c.token(ctx, device)
		if err != nil {
			return response{}, err
		}
		resp, err = c.send(ctx, device, method, path, body, token)
	}
	if err != nil {
		return response{}, err
	}
	if resp.status >= 400 {
		return resp, parseError(resp.status, resp.body)
	}
	return resp, nil
}

func (c *Client) send(ctx context.Context, device, method, path string, body []byte, token string) (response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, device+path, reader)
	if err != nil {
		return response{}, &Error{Message: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-F5-Auth-Token", token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.forget()
		return response{}, &Error{Message: err.Error()}
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		c.forget()
		return response{}, &Error{Message: "reading the answer of " + method + " " + path + ": " + err.Error()}
	}
	return response{status: resp.StatusCode, body: respBody}, nil
}

func (c *Client) token(ctx context.Context, device string) (string, error) {
	c.mu.Lock()
	token := c.tokens[device]
	c.mu.Unlock()
	if token != "" {
		return token, nil
	}
	return c.login(ctx, device)
}

func (c *Client) dropToken(device string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.tokens, device)
}

// login gets a token for a device. The credentials only travel in the request body, never in an error.
func (c *Client) login(ctx context.Context, device string) (string, error) {
	credentials, err := json.Marshal(map[string]string{
		"username": c.opts.Username, "password": c.opts.Password, "loginProviderName": c.opts.LoginProvider,
	})
	if err != nil {
		return "", &Error{Message: "encoding the login request: " + err.Error()}
	}
	resp, err := c.send(ctx, device, http.MethodPost, "/mgmt/shared/authn/login", credentials, "")
	if err != nil {
		return "", err
	}
	if resp.status >= 400 {
		loginErr := parseError(resp.status, resp.body)
		loginErr.Message = "login as " + c.opts.Username + ": " + loginErr.Message
		return "", loginErr
	}
	var answer struct {
		Token struct {
			Token string `json:"token"`
		} `json:"token"`
	}
	if err := json.Unmarshal(resp.body, &answer); err != nil || answer.Token.Token == "" {
		return "", &Error{Status: resp.status, Message: "login answer holds no token"}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tokens[device] = answer.Token.Token
	return answer.Token.Token, nil
}
