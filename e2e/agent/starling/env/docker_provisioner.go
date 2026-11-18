//go:build e2e

package env

import (
	"context"
	"fmt"

	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.yaml.in/yaml/v2"

	provisionerconfig "github.com/seagalia/flock/provisioners/config"
	"github.com/seagalia/flock/sdk/agentsdk/agentconfig"
)

const (
	// the docker client of the provisioner reads DOCKER_HOST, unset in the container, so it dials this socket
	dockerSocketInContainer = "/var/run/docker.sock"
	edsPort                 = 18003
)

type DockerProvisionerOptions struct {
	// Name is the CN of its certificate, the controller at ControllerAddr declares it under that name
	Name string
	// IP must be in the range of the agent certificates, the agents check it when they call the eds
	IP             string
	Instance       string
	ControllerAddr string
	Vip            string
}

// StartDockerProvisioner runs `flock provisioner start` with the docker block alone, on the docker daemon running the
// e2e containers. It sees every labelled container of the daemon, the specs give theirs a compose project of their own
// to keep their resource names apart.
func (e *Env) StartDockerProvisioner(ctx context.Context, o DockerProvisionerOptions) (*Provisioner, error) {
	image, err := e.imageFrom(ctx, "e2e/controller/docker/controller.Dockerfile", "controller")
	if err != nil {
		return nil, err
	}
	confYaml, err := renderDockerProvisionerConfig(o)
	if err != nil {
		return nil, err
	}
	certPem, keyPem, err := e.Certs.AgentCert(o.Name)
	if err != nil {
		return nil, fmt.Errorf("certificate of provisioner %s: %w", o.Name, err)
	}
	// the socket of the daemon as a container sees it, which differs from DOCKER_HOST of the tests on colima
	daemonSocket := testcontainers.MustExtractDockerSocket(ctx)
	c, err := e.start(ctx, o.IP, testcontainers.ContainerRequest{
		Image:          image,
		Entrypoint:     []string{"/usr/local/bin/flock", "provisioner", "start", "-c", provisionerConfigPath},
		ConfigModifier: withHostname(o.Name),
		HostConfigModifier: func(hc *container.HostConfig) {
			hc.Binds = append(hc.Binds, daemonSocket+":"+dockerSocketInContainer)
		},
		Files: []testcontainers.ContainerFile{
			fileFromBytes(confYaml, provisionerConfigPath, 0o644),
			fileFromBytes(certPem, provisionerTlsDir+"/cert.pem", 0o644),
			fileFromBytes(keyPem, provisionerTlsDir+"/key.pem", 0o600),
			fileFromBytes(e.Certs.CAPem, provisionerTlsDir+"/ca.pem", 0o644),
		},
		WaitingFor: wait.ForLog("Starting connect and http server"),
	})
	if err != nil {
		return nil, err
	}
	return &Provisioner{Container: c}, nil
}

func renderDockerProvisionerConfig(o DockerProvisionerOptions) ([]byte, error) {
	conf := provisionerconfig.Config{
		Log: agentconfig.Log{Level: "debug"},
		ControllerConfig: &agentconfig.ControllerConfig{
			Addr:         o.ControllerAddr,
			ServerName:   ControllerServerName,
			InstanceUuid: o.Instance,
		},
		TlsConfig: &agentconfig.TlsConfig{
			CertFile: provisionerTlsDir + "/cert.pem",
			KeyFile:  provisionerTlsDir + "/key.pem",
			CaFile:   provisionerTlsDir + "/ca.pem",
		},
		Docker: provisionerconfig.DockerConfig{
			Enabled: true,
			Vip:     o.Vip,
			Eds: provisionerconfig.EdsConfig{
				ListenPort:   edsPort,
				AdvertiseUrl: fmt.Sprintf("grpcs://%s:%d", o.IP, edsPort),
			},
		},
	}
	return yaml.Marshal(conf)
}
