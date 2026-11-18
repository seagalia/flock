package models

import (
	"fmt"
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/maps"
	"github.com/moby/moby/api/types/container"
)

const (
	// LabelPrefix follows the docker convention of prefixing labels with a reverse DNS name.
	LabelPrefix         = "io.seagalia.flock."
	composeProjectLabel = "com.docker.compose.project"
	composeServiceLabel = "com.docker.compose.service"
)

// Decode reads the io.seagalia.flock.* labels of a container. It answers nil when the container didn't opt in with
// io.seagalia.flock.enable. The resources are named after the compose <project>-<service>, which every replica of a
// service shares, so a container out of compose is refused.
func Decode(summary container.Summary) (*Base, error) {
	flat := make(map[string]any, len(summary.Labels))
	for key, value := range summary.Labels {
		param, hasPrefix := strings.CutPrefix(key, LabelPrefix)
		if hasPrefix {
			flat[param] = value
		}
	}

	base := &Base{}
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		// every label value is a string, the plain bool fields need it converted
		WeaklyTypedInput: true,
		Result:           base,
	})
	if err != nil {
		return nil, fmt.Errorf("create labels decoder: %w", err)
	}
	err = decoder.Decode(maps.Unflatten(flat, "."))
	if err != nil {
		return nil, fmt.Errorf("decode labels of container %s: %w", summary.ID, err)
	}
	if !base.Enable {
		return nil, nil
	}
	project := summary.Labels[composeProjectLabel]
	service := summary.Labels[composeServiceLabel]
	if project == "" || service == "" {
		return nil, fmt.Errorf("container %s has no %s or %s label", summary.ID, composeProjectLabel, composeServiceLabel)
	}
	base.Name = project + "-" + service
	base.Service = service
	return base, nil
}
