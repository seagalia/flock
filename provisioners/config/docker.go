package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const (
	defaultDockerVip       = "local"
	defaultDockerHttpPort  = 80
	defaultDockerHttpsPort = 443
	defaultEdsListenHost   = "0.0.0.0"
	defaultEdsListenPort   = 18003
	edsSchemeGrpc          = "grpc"
	edsSchemeGrpcWithTls   = "grpcs"
)

// DockerConfig turns the labelled containers of a docker host into flock resources.
type DockerConfig struct {
	Enabled bool `yaml:"enabled"`
	// Host is the address of the docker daemon, DOCKER_HOST when empty
	Host string `yaml:"host"`
	// Vip is the name of the VIP the virtual servers are attached to
	Vip       string `yaml:"vip"`
	HttpPort  uint32 `yaml:"http_port"`
	HttpsPort uint32 `yaml:"https_port"`
	// DefaultDomain gives a container with no io.seagalia.flock.domains label the domain <service>.<default_domain>,
	// only <service> when empty
	DefaultDomain string    `yaml:"default_domain"`
	Eds           EdsConfig `yaml:"eds"`
}

// EdsConfig is the endpoint discovery server the backends of the containers read their endpoints from. It listens apart
// from the provisioner server since the agents call it with no client certificate.
type EdsConfig struct {
	ListenHost string `yaml:"listen_host"`
	ListenPort uint16 `yaml:"listen_port"`
	// AdvertiseUrl is the url the agents reach the server on, grpc:// or grpcs://, grpcs://localhost:<listen_port> when
	// empty: flock dev runs the agent beside the provisioner, and the certificate it makes covers localhost
	AdvertiseUrl string `yaml:"advertise_url"`
}

func (c *DockerConfig) UnmarshalYAML(unmarshal func(any) error) error {
	type plain DockerConfig
	err := unmarshal((*plain)(c))
	if err != nil {
		return err
	}
	// a domain written .example.com would give <service>..example.com
	c.DefaultDomain = strings.TrimPrefix(c.DefaultDomain, ".")
	if c.Vip == "" {
		c.Vip = defaultDockerVip
	}
	if c.HttpPort == 0 {
		c.HttpPort = defaultDockerHttpPort
	}
	if c.HttpsPort == 0 {
		c.HttpsPort = defaultDockerHttpsPort
	}
	if c.Eds.ListenHost == "" {
		c.Eds.ListenHost = defaultEdsListenHost
	}
	if c.Eds.ListenPort == 0 {
		c.Eds.ListenPort = defaultEdsListenPort
	}
	if c.Eds.AdvertiseUrl == "" {
		c.Eds.AdvertiseUrl = fmt.Sprintf("%s://localhost:%d", edsSchemeGrpcWithTls, c.Eds.ListenPort)
	}
	if !c.Enabled {
		return nil
	}
	if c.HttpPort == c.HttpsPort {
		return errors.New("docker http_port and https_port must be different")
	}
	return checkAdvertiseUrl(c.Eds.AdvertiseUrl)
}

func checkAdvertiseUrl(advertiseUrl string) error {
	parsed, err := url.Parse(advertiseUrl)
	if err != nil {
		return fmt.Errorf("parse docker eds.advertise_url: %w", err)
	}
	isGrpc := parsed.Scheme == edsSchemeGrpc || parsed.Scheme == edsSchemeGrpcWithTls
	if !isGrpc {
		return fmt.Errorf("docker eds.advertise_url must be grpc:// or grpcs://, got %s", advertiseUrl)
	}
	if parsed.Hostname() == "" {
		return fmt.Errorf("docker eds.advertise_url has no host: %s", advertiseUrl)
	}
	return nil
}
