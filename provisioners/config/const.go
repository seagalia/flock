package config

import (
	"fmt"

	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
)

const (
	AppName        = "provisioner"
	ConfigPatchEnv = "FLOCK_PROVISIONER_CONFIG_PATCH"
)

type MetadataKey string
type ProvisionerName string

const (
	IdentifierKey MetadataKey = "flock.provisioner"
)

const (
	ACMEProvisionerName   ProvisionerName = "acme"
	DockerProvisionerName ProvisionerName = "docker"
)

func MakeProvisionerFilters(name ProvisionerName) svccore.ListFilters {
	return svccore.ListFilters{
		{
			Key:        fmt.Sprintf("%s.%s", IdentifierKey, name),
			Value:      "true",
			Operator:   svccore.ListFilter_EQUAL,
			IsMetadata: true,
		},
	}
}

func MakeProvisionerMetadata(name ProvisionerName) (string, string) {
	return fmt.Sprintf("%s.%s", IdentifierKey, name), "true"
}
