package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/knadh/koanf/maps"
	"go.yaml.in/yaml/v2"
)

// ReadFile reads the config file at path and merges over it the yaml of the env var envName when it is set, so a
// container changes a few keys with no config file of its own. Maps merge key by key, any other value of the env var
// replaces the one of the file, a list included.
func ReadFile(path, envName string) ([]byte, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	patch := os.Getenv(envName)
	if strings.TrimSpace(patch) == "" {
		return content, nil
	}

	merged, err := yamlMapping(content)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	patchMapping, err := yamlMapping([]byte(patch))
	if err != nil {
		return nil, fmt.Errorf("parse %s, it must be a yaml mapping: %w", envName, err)
	}
	maps.Merge(patchMapping, merged)
	return yaml.Marshal(merged)
}

// yamlMapping gives nested maps string keys, which maps.Merge requires and yaml.v2 doesn't produce below the top level
func yamlMapping(content []byte) (map[string]any, error) {
	mapping := map[string]any{}
	err := yaml.Unmarshal(content, &mapping)
	if err != nil {
		return nil, err
	}
	maps.IntfaceKeysToStrings(mapping)
	return mapping, nil
}
