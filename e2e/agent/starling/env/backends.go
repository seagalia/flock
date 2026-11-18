//go:build e2e

package env

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	apipb "github.com/osrg/gobgp/v4/api"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	socatImage       = "alpine/socat:1.8.1.3"
	nginxImage       = "nginx:1.31-alpine"
	mockOidcImage    = "ghcr.io/navikt/mock-oauth2-server:6.1.2"
	oauth2ProxyImage = "quay.io/oauth2-proxy/oauth2-proxy:v7.15.6"

	mockOidcPort    = "8080/tcp"
	oauth2ProxyPort = "4180/tcp"
	// oauth2-proxy refuses a cookie secret that isn't 16, 24 or 32 bytes long
	oauth2ProxyCookieSecret = "flock-e2e-cookie-secret-32-bytes"

	MockOidcUser  = "carol"
	MockOidcEmail = "carol@example.org"
)

// StartUdpEcho answers its hostname to every datagram on port 9000
func (e *Env) StartUdpEcho(ctx context.Context, name, ip string) (testcontainers.Container, error) {
	return e.start(ctx, ip, testcontainers.ContainerRequest{
		Image:          socatImage,
		Cmd:            []string{"-v", "UDP4-RECVFROM:9000,fork", "SYSTEM:hostname"},
		ConfigModifier: withHostname(name),
	})
}

// StartProxyProtocolBackend requires a proxy protocol header on port 8081, /pp-client answers the client
// address it carries, anything else is proxied to upstreams
func (e *Env) StartProxyProtocolBackend(ctx context.Context, ip string, upstreams []string) (testcontainers.Container, error) {
	servers := make([]string, 0, len(upstreams))
	for _, upstream := range upstreams {
		servers = append(servers, fmt.Sprintf("    server %s;", upstream))
	}
	conf := fmt.Sprintf(`upstream up {
%s
}
server {
    listen 8081 proxy_protocol;
    location = /pp-client {
        default_type text/plain;
        return 200 "$proxy_protocol_addr";
    }
    location / {
        proxy_pass http://up;
    }
}
`, strings.Join(servers, "\n"))
	return e.start(ctx, ip, testcontainers.ContainerRequest{
		Image:          nginxImage,
		ConfigModifier: withHostname("pp-backend"),
		Files:          []testcontainers.ContainerFile{fileFromBytes([]byte(conf), "/etc/nginx/conf.d/default.conf", 0o644)},
		WaitingFor:     wait.ForLog("start worker process"),
	})
}

// StartAuthStub answers external auth checks on port 8090 at /check, by the value of the request header x-test-auth
func (e *Env) StartAuthStub(ctx context.Context, ip string, opts ...ContainerOpt) (testcontainers.Container, error) {
	conf := `server {
    listen 8090;
    location = /check {
        if ($http_x_test_auth = allow) {
            add_header x-auth-request-user alice always;
            add_header x-seen-uri $http_x_forwarded_uri always;
            add_header x-seen-method $http_x_forwarded_method always;
            add_header x-seen-proto $http_x_forwarded_proto always;
            add_header x-seen-for $http_x_forwarded_for always;
            return 200;
        }
        if ($http_x_test_auth = deny) {
            add_header www-authenticate Bearer always;
            return 401 "denied";
        }
        if ($http_x_test_auth = redirect) {
            add_header set-cookie a=1 always;
            add_header set-cookie b=2 always;
            add_header x-hidden 1 always;
            return 302 http://login.example/;
        }
        if ($http_x_test_auth = accepted) {
            return 202;
        }
        if ($http_x_test_auth = error) {
            return 500;
        }
        return 403;
    }
}
`
	req := testcontainers.ContainerRequest{
		Image:          nginxImage,
		ConfigModifier: withHostname("auth-stub"),
		Files:          []testcontainers.ContainerFile{fileFromBytes([]byte(conf), "/etc/nginx/conf.d/default.conf", 0o644)},
		WaitingFor:     wait.ForLog("start worker process"),
	}
	for _, opt := range opts {
		opt(&req)
	}
	return e.start(ctx, ip, req)
}

// StartSilentTcp accepts connections on port 8091 and never answers on them
func (e *Env) StartSilentTcp(ctx context.Context, ip string) (testcontainers.Container, error) {
	return e.start(ctx, ip, testcontainers.ContainerRequest{
		Image:          socatImage,
		Cmd:            []string{"-d", "-d", "TCP-LISTEN:8091,fork,reuseaddr", "SYSTEM:sleep 60"},
		ConfigModifier: withHostname("silent-tcp"),
		WaitingFor:     wait.ForLog("listening on"),
	})
}

// StartMockOidc serves the issuer http://<ip>:8080/default, whose authorize endpoint logs MockOidcUser in with no form
func (e *Env) StartMockOidc(ctx context.Context, ip string) (testcontainers.Container, error) {
	conf, err := json.Marshal(map[string]any{
		"interactiveLogin": false,
		"tokenCallbacks": []map[string]any{{
			"issuerId": "default",
			"requestMappings": []map[string]any{{
				"requestParam": "grant_type", "match": "authorization_code",
				"claims": map[string]any{"sub": MockOidcUser, "email": MockOidcEmail, "email_verified": true},
			}},
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal mock oidc config: %w", err)
	}
	return e.start(ctx, ip, testcontainers.ContainerRequest{
		Image:          mockOidcImage,
		ConfigModifier: withHostname("mock-oidc"),
		ExposedPorts:   []string{mockOidcPort},
		Env:            map[string]string{"JSON_CONFIG": string(conf)},
		// the server logs nothing once it listens
		WaitingFor: wait.ForHTTP("/isalive").WithPort(mockOidcPort),
	})
}

// StartOauth2Proxy answers 200 on / to a logged in session, with the user in the x-auth-request-* headers, and
// redirects a browser to issuer otherwise. vipHost is where the browser reaches it, for the callback.
func (e *Env) StartOauth2Proxy(ctx context.Context, ip, issuer, vipHost string) (testcontainers.Container, error) {
	return e.start(ctx, ip, testcontainers.ContainerRequest{
		Image:          oauth2ProxyImage,
		ConfigModifier: withHostname("oauth2-proxy"),
		ExposedPorts:   []string{oauth2ProxyPort},
		Cmd: []string{
			"--http-address=0.0.0.0:4180",
			"--provider=oidc",
			"--oidc-issuer-url=" + issuer,
			"--client-id=flock",
			"--client-secret=flock",
			"--cookie-secret=" + oauth2ProxyCookieSecret,
			"--cookie-secure=false",
			"--email-domain=*",
			"--upstream=static://200",
			"--reverse-proxy",
			"--set-xauthrequest",
			"--skip-provider-button",
			"--redirect-url=http://" + vipHost + "/oauth2/callback",
			"--insecure-oidc-allow-unverified-email",
		},
		// it logs nothing once it listens
		WaitingFor: wait.ForHTTP("/ping").WithPort(oauth2ProxyPort),
	})
}

type GobgpPeer struct {
	Container testcontainers.Container
}

type BgpRoute struct {
	Prefix      string
	NextHop     string
	Communities []string
}

// StartGobgpPeer waits passively for the agent session
func (e *Env) StartGobgpPeer(ctx context.Context, ip, agentIP string, agentAsn, peerAsn uint32, withBfd bool) (*GobgpPeer, error) {
	image, err := e.image(ctx, "gobgp")
	if err != nil {
		return nil, err
	}
	bfdConf := ""
	if withBfd {
		bfdConf = `
  [neighbors.bfd.config]
    enabled = true
    desired-minimum-tx-interval = 300000
    required-minimum-receive = 300000
    detection-multiplier = 3`
	}
	conf := fmt.Sprintf(`[global.config]
  as = %d
  router-id = "%s"
[[neighbors]]
%s
  [neighbors.config]
    neighbor-address = "%s"
    peer-as = %d
  [neighbors.transport.config]
    passive-mode = true
  [[neighbors.afi-safis]]
    [neighbors.afi-safis.config]
      afi-safi-name = "ipv4-unicast"
`, peerAsn, ip, bfdConf, agentIP, agentAsn)
	c, err := e.start(ctx, ip, testcontainers.ContainerRequest{
		Image:          image,
		ConfigModifier: withHostname("bgp-peer"),
		Files:          []testcontainers.ContainerFile{fileFromBytes([]byte(conf), "/etc/gobgpd.toml", 0o644)},
	})
	if err != nil {
		return nil, err
	}
	return &GobgpPeer{Container: c}, nil
}

func (p *GobgpPeer) Rib(ctx context.Context) ([]BgpRoute, error) {
	out, err := execIn(ctx, p.Container, "gobgp", "global", "rib", "-a", "ipv4", "-j")
	if err != nil {
		return nil, err
	}
	var rib map[string][]struct {
		Attrs []struct {
			Type        int      `json:"type"`
			NextHop     string   `json:"nexthop"`
			Communities []uint32 `json:"communities"`
		} `json:"attrs"`
	}
	if err := json.Unmarshal([]byte(out), &rib); err != nil {
		return nil, fmt.Errorf("invalid gobgp rib %q: %w", out, err)
	}
	routes := make([]BgpRoute, 0)
	for prefix, paths := range rib {
		for _, path := range paths {
			route := BgpRoute{Prefix: prefix, Communities: []string{}}
			for _, attr := range path.Attrs {
				if attr.NextHop != "" {
					route.NextHop = attr.NextHop
				}
				for _, community := range attr.Communities {
					route.Communities = append(route.Communities, fmt.Sprintf("%d:%d", community>>16, community&0xffff))
				}
			}
			routes = append(routes, route)
		}
	}
	return routes, nil
}

// BfdSessionState gives the state of the bfd session the peer runs with the neighbor
func (p *GobgpPeer) BfdSessionState(ctx context.Context, neighbor string) (apipb.BfdSessionState, error) {
	out, err := execIn(ctx, p.Container, "gobgp", "neighbor", neighbor, "-j")
	if err != nil {
		return apipb.BfdSessionState_BFD_SESSION_STATE_UNSPECIFIED, err
	}
	var peer struct {
		State struct {
			BfdState struct {
				SessionState apipb.BfdSessionState `json:"session_state"`
			} `json:"bfd_state"`
		} `json:"state"`
	}
	if err := json.Unmarshal([]byte(out), &peer); err != nil {
		return apipb.BfdSessionState_BFD_SESSION_STATE_UNSPECIFIED, fmt.Errorf("invalid gobgp neighbor %q: %w", out, err)
	}
	return peer.State.BfdState.SessionState, nil
}
