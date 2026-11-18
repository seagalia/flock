package zone

import (
	"github.com/seagalia/flock/sdk/helpers"
)

func (x *Zone) GetIdentifier() string {
	return x.GetUUID()
}

func (x *Zone) SetIdentifier(identifier string) {
	x.UUID = identifier
}

func (x *Zone) Signature() (string, error) {
	return helpers.MessageSignature(x)
}

func (x *Zone) AddMetadata(key, value string) {
	if x.Metadata == nil {
		x.Metadata = make(map[string]string)
	}
	x.Metadata[key] = value
}

func (x *Zone) DeleteMetadata(key string) map[string]string {
	if x.Metadata == nil {
		return nil
	}
	delete(x.Metadata, key)
	return x.Metadata
}

func (x *Zone) SetMetadata(m map[string]string) {
	x.Metadata = m
}

// CouldNotAllocateIP tells a zone without pools: any ip can be used there, the ipam has nothing to manage.
func (x *Zone) CouldNotAllocateIP() bool {
	return len(x.GetPools()) == 0
}

// DefaultPool is the first public pool, nil when there is none.
func (x *Zone) DefaultPool() *IpPool {
	for _, pool := range x.GetPools() {
		if pool.GetPrivacy() == IpPool_PUBLIC {
			return pool
		}
	}
	return nil
}

// FindPool looks a pool up by name when given, otherwise by the first one holding all the metadata selector entries.
func (x *Zone) FindPool(name string, metadataSelector map[string]string) *IpPool {
	if name == "" && len(metadataSelector) == 0 {
		return nil
	}
	for _, pool := range x.GetPools() {
		if name != "" {
			if pool.GetName() == name {
				return pool
			}
			continue
		}
		if pool.hasMetadata(metadataSelector) {
			return pool
		}
	}
	return nil
}

func (x *IpPool) hasMetadata(selector map[string]string) bool {
	for key, value := range selector {
		if got, found := x.GetMetadata()[key]; !found || got != value {
			return false
		}
	}
	return true
}
