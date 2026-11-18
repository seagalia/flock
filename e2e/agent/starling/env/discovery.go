//go:build e2e

package env

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	corednsImage    = "coredns/coredns:1.13.1"
	corednsConfPath = "/etc/coredns/Corefile"
	edsRoot         = "/usr/share/nginx/html"
)

// StaticEds serves a fixed answer of an endpoint discovery server on port 80: the JSON list of endpoints written by
// SetEndpoints at /<service>.json, whatever the service_name the agent adds to the query
type StaticEds struct {
	Container testcontainers.Container
}

type EdsEndpoint struct {
	IP                  string `json:"ip"`
	Port                uint32 `json:"port"`
	LoadBalancingWeight uint32 `json:"load_balancing_weight"`
}

func (e *Env) StartStaticEds(ctx context.Context, ip string) (*StaticEds, error) {
	c, err := e.start(ctx, ip, testcontainers.ContainerRequest{
		Image:          nginxImage,
		ConfigModifier: withHostname("static-eds"),
		WaitingFor:     wait.ForListeningPort("80/tcp"),
	})
	if err != nil {
		return nil, err
	}
	return &StaticEds{Container: c}, nil
}

func (s *StaticEds) SetEndpoints(ctx context.Context, service string, endpoints ...EdsEndpoint) error {
	content, err := json.Marshal(endpoints)
	if err != nil {
		return err
	}
	return s.Container.CopyToContainer(ctx, content, edsRoot+"/"+service+".json", 0o644)
}

// CoreDNS answers the names of its hosts, in /etc/hosts format, on port 53
type CoreDNS struct {
	Container testcontainers.Container
}

func (e *Env) StartCoreDNS(ctx context.Context, ip, hosts string) (*CoreDNS, error) {
	c, err := e.start(ctx, ip, testcontainers.ContainerRequest{
		Image:          corednsImage,
		Cmd:            []string{"-conf", corednsConfPath},
		ConfigModifier: withHostname("coredns"),
		Files:          []testcontainers.ContainerFile{fileFromBytes([]byte(corefile(hosts)), corednsConfPath, 0o644)},
		WaitingFor:     wait.ForLog("CoreDNS-"),
	})
	if err != nil {
		return nil, err
	}
	return &CoreDNS{Container: c}, nil
}

// SetHosts rewrites the Corefile: the files copied by testcontainers all have the same mtime, so a hosts file of
// the same size would look unchanged to the hosts plugin, the reload plugin compares the content instead
func (d *CoreDNS) SetHosts(ctx context.Context, hosts string) error {
	return d.Container.CopyToContainer(ctx, []byte(corefile(hosts)), corednsConfPath, 0o644)
}

func corefile(hosts string) string {
	return fmt.Sprintf(`.:53 {
    reload 2s 1s
    hosts {
%s
    }
    log
}
`, hosts)
}
