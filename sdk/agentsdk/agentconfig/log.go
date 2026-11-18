// Package agentconfig holds the configuration blocks every agent has: logs, the controller to connect to, the TLS
// identity of the agent and its server.
package agentconfig

import (
	"fmt"

	"github.com/seagalia/flock/sdk/logging"
)

type Log struct {
	Level            string        `yaml:"level"`
	ForceColors      bool          `yaml:"force_colors"`
	NoColor          bool          `yaml:"no_color"`
	InJson           bool          `yaml:"in_json"`
	FilterComponents []string      `yaml:"filter_components"`
	Syslog           *SyslogConfig `yaml:"syslog"`
}

func (c *Log) UnmarshalYAML(unmarshal func(any) error) error {
	type plain Log
	err := unmarshal((*plain)(c))
	if err != nil {
		return err
	}
	// applied at startup by logging.Setup, only validated here
	_, err = logging.ParseLevel(c.Level)
	return err
}

type SyslogConfig struct {
	Targets        []string                     `yaml:"targets"`
	FormatRfc3164  bool                         `yaml:"format_rfc3164"`
	StructuredData map[string]map[string]string `yaml:"structured_data"`
}

func (c *SyslogConfig) UnmarshalYAML(unmarshal func(any) error) error {
	type plain SyslogConfig
	err := unmarshal((*plain)(c))
	if err != nil {
		return err
	}
	if len(c.Targets) == 0 {
		return fmt.Errorf("syslog targets is empty")

	}
	if c.StructuredData == nil {
		c.StructuredData = make(map[string]map[string]string)
	}
	return nil
}
