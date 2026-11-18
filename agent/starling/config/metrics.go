package config

import (
	"fmt"
	"regexp"

	"github.com/seagalia/flock/sdk/config"
)

var validateNameForPath = regexp.MustCompile(`(\s|_)`).MatchString

type MetricsConfig struct {
	ProxyMetricsConfig *ProxyMetricsConfig `yaml:"proxy"`
}

func (u *MetricsConfig) init() error {
	if u.ProxyMetricsConfig == nil {
		u.ProxyMetricsConfig = &ProxyMetricsConfig{}
		err := u.ProxyMetricsConfig.init()
		if err != nil {
			return err
		}
	}
	return nil
}

type ProxyMetricsConfig struct {
	Targets []*ProxyMetricsTarget `yaml:"targets"`
}

func (u *ProxyMetricsConfig) init() error {
	if u.Targets == nil {
		u.Targets = make([]*ProxyMetricsTarget, 0)
	}
	return nil
}

type ProxyMetricsTarget struct {
	Name          string              `yaml:"name"`
	URL           URLConfig           `yaml:"url"`
	SocketPath    string              `yaml:"socket_path"`
	User          string              `yaml:"user"`
	Password      string              `yaml:"password"`
	Headers       map[string][]string `yaml:"headers"`
	ScrapeTimeout config.Duration     `yaml:"scrape_timeout"`
}

func (u *ProxyMetricsTarget) UnmarshalYAML(unmarshal func(any) error) error {
	type plain ProxyMetricsTarget
	err := unmarshal((*plain)(u))
	if err != nil {
		return err
	}
	if u.Name == "" {
		return fmt.Errorf("proxy metrics target name is empty")
	}
	if validateNameForPath(u.Name) {
		return fmt.Errorf("proxy metrics target name must not contains whitespace or underscore")
	}
	if u.URL.Raw == "" && u.SocketPath == "" {
		return fmt.Errorf("proxy metrics target url or socket path is empty")
	}
	if u.URL.Raw != "" && u.SocketPath != "" {
		return fmt.Errorf("proxy metrics target url and socket path cannot be set at the same time")
	}
	return nil
}
