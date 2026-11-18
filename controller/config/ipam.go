package config

import "fmt"

type IPAMConfig struct {
	// Disable IPAM
	Disabled bool `yaml:"disabled"`
	// Use a remote IPAM instead of db ipam used by default
	Remote *RemoteIPAMConfig `yaml:"remote"`
}

type RemoteIPAMConfig struct {
	// Url to remote ipam
	URL string `yaml:"url"`
	// Headers to remote ipam for each request
	Headers RemoteIPAMHeader `yaml:"headers"`
	// Skip TLS verification
	InsecureSkipVerify bool `yaml:"insecure_skip_verify"`
}

func (l *RemoteIPAMConfig) UnmarshalYAML(unmarshal func(any) error) error {
	type plain RemoteIPAMConfig
	err := unmarshal((*plain)(l))
	if err != nil {
		return err
	}
	if l.URL == "" {
		return fmt.Errorf("remote ipam: url is required")
	}
	return nil
}

// RemoteIPAMHeader accepts a value or a list of values per header.
type RemoteIPAMHeader map[string][]string

func (l *RemoteIPAMHeader) UnmarshalYAML(unmarshal func(any) error) error {
	var headers map[string][]string
	err := unmarshal(&headers)
	if err == nil {
		*l = headers
		return nil
	}

	var headersSimple map[string]string
	err = unmarshal(&headersSimple)
	if err != nil {
		return fmt.Errorf("remote ipam headers: %w", err)
	}
	headers = make(map[string][]string)
	for k, v := range headersSimple {
		headers[k] = []string{v}
	}
	*l = headers
	return nil
}
