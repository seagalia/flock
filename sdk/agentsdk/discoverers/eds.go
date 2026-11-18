package discoverers

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/vigie/sdk/clients"
	eds "github.com/seagalia/vigie/sdk/vigie/eds/v1"
	"github.com/seagalia/vigie/sdk/vigie/eds/v1/edsconnect"
	"google.golang.org/protobuf/encoding/protojson"
)

// edsGrpcTimeout bounds one call of the gRPC mode, like the timeout of the http client of the HTTP mode
const edsGrpcTimeout = 10 * time.Second

var grpcEndptDiscoCliFact = clients.NewEndpointDiscovery

// SetGrpcEndpointDiscoClientFactory allows to override the default grpc client factory for testing purpose
func SetGrpcEndpointDiscoClientFactory(f func(baseURL string, opts clients.Options) (edsconnect.EndpointDiscoveryServiceClient, error)) {
	grpcEndptDiscoCliFact = f
}

// NewEds picks the mode from the scheme of u: http and https get the JSON list of GET /services, grpc and grpcs call
// the EDS service of Vigie, without TLS (h2c) and over TLS.
func NewEds(u *core.URL, serviceName string, caRaw string, headers map[string]string) (Discoverer, error) {
	switch u.GetScheme() {
	case "http", "https":
		return newEdsHttp(u, serviceName, caRaw, headers), nil
	case "grpc", "grpcs":
		return newEdsGrpc(u, serviceName, caRaw, headers)
	}
	return nil, fmt.Errorf("eds url scheme %q is not one of http, https, grpc and grpcs", u.GetScheme())
}

type edsHttp struct {
	httpClient  *http.Client
	url         *core.URL
	serviceName string
	headers     map[string]string
}

func newEdsHttp(u *core.URL, serviceName string, caRaw string, headers map[string]string) *edsHttp {
	var ca *x509.CertPool
	if caRaw != "" {
		ca = x509.NewCertPool()
		ca.AppendCertsFromPEM([]byte(caRaw))
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig: &tls.Config{
			RootCAs: ca,
		},
	}
	return &edsHttp{
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   10 * time.Second,
		},
		url:         u,
		headers:     headers,
		serviceName: serviceName,
	}
}

func (d *edsHttp) Run(ctx context.Context, found chan<- []*core.IPAddr) error {
	svcParam := ""
	if d.serviceName != "" {
		svcParam = "?service_name=" + url.QueryEscape(d.serviceName)
	}
	req, err := http.NewRequest(http.MethodGet, d.url.Format()+svcParam, nil)
	if err != nil {
		return err
	}
	req = req.WithContext(ctx)

	if len(d.headers) > 0 {
		httpHeader := make(http.Header)
		for k, v := range d.headers {
			httpHeader.Add(k, v)
		}
		req.Header = httpHeader
	}

	if d.url.GetUserInfo() != nil {
		req.SetBasicAuth(d.url.GetUserInfo().GetUser(), d.url.GetUserInfo().GetPassword())
	}
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		errMess := "no content error"
		b, err := io.ReadAll(resp.Body)
		if err == nil {
			errMess = string(b)
		}
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, errMess)
	}
	rawMessages := make([]json.RawMessage, 0)
	err = json.NewDecoder(resp.Body).Decode(&rawMessages)
	if err != nil {
		return err
	}
	ips := make([]*core.IPAddr, 0)
	for _, rawMess := range rawMessages {
		ipAddr := &core.IPAddr{}
		err := protojson.UnmarshalOptions{
			AllowPartial: false,
		}.Unmarshal(rawMess, ipAddr)
		if err != nil {
			return fmt.Errorf("Failed to unmarshal: %s", err)
		}
		err = helpers.Validate(ipAddr)
		if err != nil {
			return fmt.Errorf("Failed to validate %s: %s", ipAddr.Format(), err)
		}
		ips = append(ips, ipAddr)
	}
	select {
	case found <- ips:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type edsGrpc struct {
	client      edsconnect.EndpointDiscoveryServiceClient
	serviceName string
	headers     map[string]string
}

func newEdsGrpc(u *core.URL, serviceName string, caRaw string, headers map[string]string) (*edsGrpc, error) {
	// the user info goes as basic auth, not in the base url, and the path stays as a prefix of the procedure
	baseURL := u.ToUrl()
	baseURL.User = nil
	opts := clients.Options{
		Username: u.GetUserInfo().GetUser(),
		Password: u.GetUserInfo().GetPassword(),
	}
	if u.GetScheme() == "grpc" {
		baseURL.Scheme = "http"
	} else {
		baseURL.Scheme = "https"
		var ca *x509.CertPool
		if caRaw != "" {
			ca = x509.NewCertPool()
			ca.AppendCertsFromPEM([]byte(caRaw))
		}
		opts.TLSConfig = &tls.Config{RootCAs: ca}
	}

	client, err := grpcEndptDiscoCliFact(baseURL.String(), opts)
	if err != nil {
		return nil, err
	}
	return &edsGrpc{
		client:      client,
		serviceName: serviceName,
		headers:     headers,
	}, nil
}

func (d *edsGrpc) Run(ctx context.Context, found chan<- []*core.IPAddr) error {
	// the send must not use the call's timeout ctx, or a slow reader of found would turn into a timeout error
	callCtx, cancel := context.WithTimeout(ctx, edsGrpcTimeout)
	defer cancel()
	callCtx, callInfo := connect.NewClientContext(callCtx)
	for key, value := range d.headers {
		callInfo.RequestHeader().Set(key, value)
	}
	resp, err := d.client.GetEndpoints(callCtx, &eds.GetEndpointsRequest{
		ServiceName: d.serviceName,
	})
	if err != nil {
		return err
	}
	ips := make([]*core.IPAddr, 0, len(resp.GetEndpoints()))
	for _, endpoint := range resp.GetEndpoints() {
		ipAddr := &core.IPAddr{
			Ip:                  endpoint.GetIp(),
			Port:                endpoint.GetPort(),
			LoadBalancingWeight: endpoint.GetLoadBalancingWeight(),
		}
		err = helpers.Validate(ipAddr)
		if err != nil {
			return fmt.Errorf("Failed to validate %s: %s", ipAddr.Format(), err)
		}
		ips = append(ips, ipAddr)
	}
	select {
	case found <- ips:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
