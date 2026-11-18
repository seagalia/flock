package main

import "google.golang.org/protobuf/reflect/protoreflect"

// The tables below are everything the plugin knows about the flock protos in particular. An entry they miss stops the
// generation, so a new proto construct is decided here instead of vanishing from the CRDs.

const uniqueNamesRule = "unique_names"

// reference is the CRD field a uuid field of the protos becomes, and the kind of the object it names.
type reference struct {
	jsonName string
	kind     string
}

// references maps every uuid field of the protos, by field name since the same name always points at the same kind.
var references = map[protoreflect.Name]reference{
	"backend_uuid":         {jsonName: "backendRef", kind: "Backend"},
	"certificates_uuid":    {jsonName: "certificateRefs", kind: "Certificate"},
	"instance_uuid":        {jsonName: "instanceRef", kind: "Instance"},
	"secret_uuid":          {jsonName: "secretRef", kind: "Secret"},
	"vips_uuid":            {jsonName: "vipRefs", kind: "VIP"},
	"virtual_servers_uuid": {jsonName: "virtualServerRefs", kind: "VirtualServer"},
	"zone_uuid":            {jsonName: "zoneRef", kind: "Zone"},
}

// celExpressions rewrites the CEL rules of the protos for the apiserver, by rule id: protovalidate names fields in
// snake_case from `this`, the apiserver in camelCase from `self`, and reading an absent field fails there instead of
// giving the zero value.
var celExpressions = map[string]string{
	"external_auth.upstream_headers.forbidden": "!(self in ['host', 'connection', 'transfer-encoding', 'te', 'upgrade', " +
		"'keep-alive', 'proxy-connection', 'trailer', 'content-length']) && !self.startsWith(':')",
	"eds.url.scheme":    "self.scheme in ['http', 'https', 'grpc', 'grpcs']",
	"ip_pool.addresses": "(has(self.subnets) && self.subnets.size() > 0) || (has(self.ipRanges) && self.ipRanges.size() > 0)",
	"user_info.user":    "!has(self.passwordFromSecret) || (has(self.user) && self.user.size() > 0)",
}

// rootDrops are the fields of every root the object gives instead: `metadata.name`, `status.uuid` and the
// `flock.seagalia.io/` annotations.
var rootDrops = []protoreflect.Name{"UUID", "name", "metadata"}

// override is how a CRD type differs from its proto message.
type override struct {
	drop []protoreflect.Name
	// refs are references the CRD adds, which ToProto leaves to the reconciler
	refs []extraRef
}

type extraRef struct {
	jsonName string
	kind     string
	doc      string
}

var overrides = map[protoreflect.FullName]override{
	// only agents write the password itself, the CRD points at a secret with passwordFromSecret
	"flock.api.config.core.v1.URL.UserInfo": {drop: []protoreflect.Name{"password"}},
	// the certificate and its key stay in a Kubernetes Secret, the reconciler copies them into the flock certificate
	"flock.api.config.cert.v1.Certificate": {
		drop: []protoreflect.Name{"cert_chain", "private_key", "ca"},
		refs: []extraRef{{
			jsonName: "secretRef",
			kind:     "Secret",
			doc: "secretRef is the kubernetes.io/tls Secret holding the certificate chain (tls.crt), its private key " +
				"(tls.key) and the CA (ca.crt, optional), in the namespace of the provisioner.",
		}},
	},
}

// recursionDepths unrolls the messages holding themselves, which a CRD schema can't express, into one type per level.
var recursionDepths = map[protoreflect.FullName]int{
	"flock.extensions.limit.starling_limit.v1.Descriptor": 4,
}

// typeRenames settles two messages of different packages whose Go names collide.
var typeRenames = map[protoreflect.FullName]string{
	"flock.api.config.limit.v1.Config": "LimitConfig",
}
