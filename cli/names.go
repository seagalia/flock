package cli

import "github.com/alecthomas/kong"

type Named interface {
	IsUUID() bool
	Identifier() string
}

// NameUuid is a flag or argument value that is either a resource name or its uuid.
type NameUuid struct {
	identifier string
}

func (n *NameUuid) Decode(ctx *kong.DecodeContext) error {
	return ctx.Scan.PopValueInto("value", &n.identifier)
}

func (n *NameUuid) IsUUID() bool {
	return isUuid(n.identifier)
}

func (n *NameUuid) Identifier() string {
	return n.identifier
}

func isUuid(s string) bool {
	if len(s) != 36 {
		return false
	}
	return isUuidRegex.MatchString(s)
}

type InstanceNamed struct {
	NameUuid
}

type CertNamed struct {
	NameUuid
}

type SecretNamed struct {
	NameUuid
}

type BackendNamed struct {
	NameUuid
}

type VipNamed struct {
	NameUuid
}

type VsNamed struct {
	NameUuid
}

type ZoneNamed struct {
	NameUuid
}
