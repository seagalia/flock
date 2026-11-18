//go:build e2e

package env

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/textproto"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
)

const (
	clientCAPath = "/etc/e2e/ca.pem"
	statusMarker = "\n__status__:"
)

type Client struct {
	Container testcontainers.Container
}

func (e *Env) StartClient(ctx context.Context, ip string) (*Client, error) {
	image, err := e.image(ctx, "client")
	if err != nil {
		return nil, err
	}
	c, err := e.start(ctx, ip, testcontainers.ContainerRequest{
		Image:          image,
		ConfigModifier: withHostname("client"),
		HostConfigModifier: func(hc *container.HostConfig) {
			hc.CapAdd = append(hc.CapAdd, "NET_ADMIN", "NET_RAW")
		},
		Files: []testcontainers.ContainerFile{fileFromBytes(e.Certs.CAPem, clientCAPath, 0o644)},
	})
	if err != nil {
		return nil, err
	}
	return &Client{Container: c}, nil
}

func (c *Client) Exec(ctx context.Context, cmd ...string) (string, error) {
	return execIn(ctx, c.Container, cmd...)
}

func (c *Client) AddRoute(ctx context.Context, cidr, via string) error {
	_, err := c.Exec(ctx, "ip", "route", "replace", cidr, "via", via)
	return err
}

type CurlOpt func() []string

func WithResolve(host string, port int, ip string) CurlOpt {
	return func() []string { return []string{"--resolve", fmt.Sprintf("%s:%d:%s", host, port, ip)} }
}

func WithCA() CurlOpt {
	return func() []string { return []string{"--cacert", clientCAPath} }
}

func WithHeader(header string) CurlOpt {
	return func() []string { return []string{"-H", header} }
}

// WithBodyFile posts the file of the client container at path.
func WithBodyFile(path, contentType string) CurlOpt {
	return func() []string { return []string{"--data-binary", "@" + path, "-H", "content-type: " + contentType} }
}

// WithHttp2 asks for HTTP/2 over TLS, curl falls back to HTTP/1.1 when the server doesn't offer h2 in ALPN.
func WithHttp2() CurlOpt {
	return func() []string { return []string{"--http2"} }
}

func WithMaxTime(d time.Duration) CurlOpt {
	return func() []string { return []string{"--max-time", strconv.Itoa(int(d.Seconds()))} }
}

func (c *Client) HTTPGet(ctx context.Context, url string, opts ...CurlOpt) (int, string, error) {
	args := []string{"curl", "-s", "--max-time", "5", "-w", statusMarker + "%{http_code}"}
	for _, opt := range opts {
		args = append(args, opt()...)
	}
	args = append(args, url)
	out, err := c.Exec(ctx, args...)
	if err != nil {
		return 0, out, err
	}
	idx := strings.LastIndex(out, statusMarker)
	if idx < 0 {
		return 0, out, fmt.Errorf("no status in curl output: %s", out)
	}
	status, err := strconv.Atoi(strings.TrimSpace(out[idx+len(statusMarker):]))
	return status, out[:idx], err
}

// Login follows every redirect with a cookie jar of its own, so each call logs in from scratch, and gives the status
// and the body of the last answer
func (c *Client) Login(ctx context.Context, url string, opts ...CurlOpt) (int, string, error) {
	jar := fmt.Sprintf("/tmp/cookies-%d", time.Now().UnixNano())
	args := []string{"curl", "-s", "-L", "--max-time", "10", "-c", jar, "-b", jar, "-w", statusMarker + "%{http_code}"}
	for _, opt := range opts {
		args = append(args, opt()...)
	}
	args = append(args, url)
	out, err := c.Exec(ctx, args...)
	if err != nil {
		return 0, out, err
	}
	idx := strings.LastIndex(out, statusMarker)
	if idx < 0 {
		return 0, out, fmt.Errorf("no status in curl output: %s", out)
	}
	status, err := strconv.Atoi(strings.TrimSpace(out[idx+len(statusMarker):]))
	return status, out[:idx], err
}

// HTTPGetHeaders never follows a redirect
func (c *Client) HTTPGetHeaders(ctx context.Context, url string, opts ...CurlOpt) (int, http.Header, string, error) {
	args := []string{"curl", "-s", "--max-time", "5", "-D", "-"}
	for _, opt := range opts {
		args = append(args, opt()...)
	}
	args = append(args, url)
	out, err := c.Exec(ctx, args...)
	if err != nil {
		return 0, nil, out, err
	}
	// curl prints the body decoded, so only the head is parsed as http, a chunked body would not read back
	head, body, found := strings.Cut(out, "\r\n\r\n")
	if !found {
		return 0, nil, out, fmt.Errorf("no headers in curl output: %s", out)
	}
	reader := textproto.NewReader(bufio.NewReader(strings.NewReader(head + "\r\n\r\n")))
	statusLine, err := reader.ReadLine()
	if err != nil {
		return 0, nil, out, fmt.Errorf("read status line of %q: %w", out, err)
	}
	statusFields := strings.Fields(statusLine)
	if len(statusFields) < 2 {
		return 0, nil, out, fmt.Errorf("invalid status line %q", statusLine)
	}
	status, err := strconv.Atoi(statusFields[1])
	if err != nil {
		return 0, nil, out, fmt.Errorf("invalid status line %q: %w", statusLine, err)
	}
	headers, err := reader.ReadMIMEHeader()
	if err != nil {
		return 0, nil, out, fmt.Errorf("read headers of %q: %w", out, err)
	}
	return status, http.Header(headers), body, nil
}

func (c *Client) Redirect(ctx context.Context, url string, opts ...CurlOpt) (int, string, error) {
	args := []string{"curl", "-s", "--max-time", "5", "-o", "/dev/null", "-w", "%{http_code} %{redirect_url}"}
	for _, opt := range opts {
		args = append(args, opt()...)
	}
	args = append(args, url)
	out, err := c.Exec(ctx, args...)
	if err != nil {
		return 0, "", err
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return 0, "", fmt.Errorf("empty curl output")
	}
	status, err := strconv.Atoi(fields[0])
	location := ""
	if len(fields) > 1 {
		location = fields[1]
	}
	return status, location, err
}

func (c *Client) Hostname(ctx context.Context, url string, opts ...CurlOpt) (string, error) {
	status, body, err := c.HTTPGet(ctx, url, opts...)
	if err != nil {
		return "", err
	}
	if status != 200 {
		return "", fmt.Errorf("status %d: %s", status, body)
	}
	var resp struct {
		Hostname string `json:"hostname"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return "", fmt.Errorf("invalid hostname response %q: %w", body, err)
	}
	return resp.Hostname, nil
}

// WebsocketEcho opens a websocket on url, sends message and gives what the server sent back
func (c *Client) WebsocketEcho(ctx context.Context, url, message string, opts ...CurlOpt) (string, error) {
	args := []string{"curl", "-s", "--max-time", "2", "-T", "-"}
	for _, opt := range opts {
		args = append(args, opt()...)
	}
	args = append(args, url)
	// curl sends its stdin as the websocket message, then keeps listening until --max-time (exit code 28):
	// a timeout is the normal end. sh gets the message as $0 and the curl command as $@.
	script := `printf %s "$0" | "$@"; code=$?; if [ "$code" -eq 28 ]; then exit 0; fi; exit "$code"`
	return c.Exec(ctx, append([]string{"sh", "-c", script, message}, args...)...)
}

var subjectCN = regexp.MustCompile(`CN\s*=\s*([^,/\n]+)`)

// PeerCertCN sends no sni when sni is empty
func (c *Client) PeerCertCN(ctx context.Context, ip string, port int, sni string) (string, error) {
	serverName := "-noservername"
	if sni != "" {
		serverName = "-servername " + sni
	}
	out, err := c.Exec(ctx, "sh", "-c", fmt.Sprintf(
		"openssl s_client -connect %s:%d %s </dev/null 2>/dev/null | openssl x509 -noout -subject", ip, port, serverName))
	if err != nil {
		return "", err
	}
	match := subjectCN.FindStringSubmatch(out)
	if match == nil {
		return "", fmt.Errorf("no certificate served: %s", out)
	}
	return strings.TrimSpace(match[1]), nil
}

func (c *Client) UDP(ctx context.Context, addr, payload string) (string, error) {
	out, err := c.Exec(ctx, "sh", "-c", fmt.Sprintf("echo %s | socat -t2 - UDP4:%s", payload, addr))
	// replies are one line, socat 1.8.1 prints a reply received after stdin is closed twice
	firstLine, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	return firstLine, err
}

var macRe = regexp.MustCompile(`([0-9a-fA-F]{2}(:[0-9a-fA-F]{2}){5})`)

func (c *Client) Neigh(ctx context.Context, ip string) string {
	out, _ := c.Exec(ctx, "ip", "neigh", "show", ip)
	return strings.ToLower(macRe.FindString(out))
}

func (c *Client) Arping(ctx context.Context, ip string) (string, error) {
	out, err := c.Exec(ctx, "arping", "-c", "1", "-w", "2", "-I", "eth0", ip)
	if err != nil {
		return "", err
	}
	mac := macRe.FindString(out)
	if mac == "" {
		return "", fmt.Errorf("no arp reply: %s", out)
	}
	return strings.ToLower(mac), nil
}

type LoopResult struct {
	At time.Time
	OK bool
}

// Loop measures how long traffic is broken during a change
type Loop struct {
	mutex   *sync.Mutex
	results []LoopResult
	cancel  context.CancelFunc
	done    chan struct{}
}

func (c *Client) StartLoop(ctx context.Context, url string, every time.Duration, opts ...CurlOpt) *Loop {
	loopCtx, cancel := context.WithCancel(ctx)
	loop := &Loop{mutex: &sync.Mutex{}, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(loop.done)
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			// a request is dated when it starts, so one sent before a change is never counted after it
			sentAt := time.Now()
			status, _, err := c.HTTPGet(loopCtx, url, append(opts, WithMaxTime(2*time.Second))...)
			isSuccess := err == nil && status >= 200 && status < 300
			loop.mutex.Lock()
			loop.results = append(loop.results, LoopResult{At: sentAt, OK: isSuccess})
			loop.mutex.Unlock()
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return loop
}

func (l *Loop) Stop() {
	l.cancel()
	<-l.done
}

func (l *Loop) Results() []LoopResult {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return append([]LoopResult{}, l.results...)
}

// Recovery describes traffic since a change
type Recovery struct {
	// BrokenFor is how long requests failed before succeeding again for good
	BrokenFor time.Duration
	Failed    int
	Recovered bool
}

func (l *Loop) RecoveryAfter(since time.Time) Recovery {
	failed := 0
	var lastFailure, lastSuccess time.Time
	for _, result := range l.Results() {
		if result.At.Before(since) {
			continue
		}
		if result.OK {
			lastSuccess = result.At
			continue
		}
		failed++
		lastFailure = result.At
	}
	if lastSuccess.IsZero() {
		return Recovery{Failed: failed}
	}
	if failed == 0 {
		return Recovery{Recovered: true}
	}
	return Recovery{
		BrokenFor: lastFailure.Sub(since),
		Failed:    failed,
		Recovered: lastSuccess.After(lastFailure),
	}
}
