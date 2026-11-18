package secret

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	starling_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/starling_options/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

// FoundRef is one secret reference of a resource. Path names where it sits: `metadata["<key>"]` for a top-level
// metadata value, otherwise the proto field names leading to the header value or the url user info holding the
// reference (`eds.headers[1]`). Resolving only switches a oneof case, so a path found in a received resource also finds
// the field in its resolved copy.
type FoundRef struct {
	Path string
	Ref  Ref
}

// secretField is one place of a resource where a value can come from a secret, a header value or the user info of a
// url, the other one nil.
type secretField struct {
	path     string
	header   *core.HeaderValue
	userInfo *core.URL_UserInfo
}

func (f secretField) keyRef() *flocktype.SecretKeyRef {
	if f.header != nil {
		return f.header.GetValueFromSecret()
	}
	return f.userInfo.GetPasswordFromSecret()
}

func (f secretField) setValue(value string) {
	if f.header != nil {
		f.header.ValueSource = &core.HeaderValue_Value{Value: value}
		return
	}
	f.userInfo.PasswordSource = &core.URL_UserInfo_Password{Password: value}
}

func (f secretField) setKeyRef(keyRef *flocktype.SecretKeyRef) {
	if f.header != nil {
		f.header.ValueSource = &core.HeaderValue_ValueFromSecret{ValueFromSecret: keyRef}
		return
	}
	f.userInfo.PasswordSource = &core.URL_UserInfo_PasswordFromSecret{PasswordFromSecret: keyRef}
}

// resourceFields are the secret fields of a resource. Those of the starling HTTP options of a virtual server point
// into options, a copy unpacked from the Any packed, so a change there only reaches the resource through packOptions.
type resourceFields struct {
	fields  []secretField
	options *starling_vsopts.HttpOptions
	packed  *anypb.Any
}

// packOptions marshals deterministically, so a resource rewritten twice the same way gives the same bytes.
func (r resourceFields) packOptions() error {
	if r.options == nil {
		return nil
	}
	return anypb.MarshalFrom(r.packed, r.options, proto.MarshalOptions{Deterministic: true})
}

// secretFieldsOf lists by name every place a resource can hold a value from a secret. The spec "Places of a secret
// value" reads the protos of the api and fails when a new field leads to a header value or a url password.
func secretFieldsOf(m proto.Message) (resourceFields, error) {
	switch resource := m.(type) {
	case *backend.Backend:
		return resourceFields{fields: backendFields(resource)}, nil
	case *vs.VirtualServer:
		return virtualServerFields(resource)
	}
	return resourceFields{}, nil
}

func backendFields(aBackend *backend.Backend) []secretField {
	var fields []secretField
	userInfo := aBackend.GetEds().GetUrl().GetUserInfo()
	if userInfo != nil {
		fields = append(fields, secretField{path: "eds.url.user_info", userInfo: userInfo})
	}
	for i, header := range aBackend.GetEds().GetHeaders() {
		if header != nil {
			fields = append(fields, secretField{path: fmt.Sprintf("eds.headers[%d]", i), header: header})
		}
	}
	healthCheckHeaders := aBackend.GetHealthCheck().GetHttpHealthCheck().GetRequestHeadersToAdd()
	return appendHeaderOptions(fields, "health_check.http_health_check.request_headers_to_add", healthCheckHeaders)
}

// virtualServerFields reads the starling HTTP options only, the one extension of a virtual server holding headers.
func virtualServerFields(aVs *vs.VirtualServer) (resourceFields, error) {
	const optionsPath = "http_routes_config.typed_options"
	packed := aVs.GetHttpRoutesConfig().GetTypedOptions()
	if packed == nil || !packed.MessageIs(&starling_vsopts.HttpOptions{}) {
		return resourceFields{}, nil
	}
	options := &starling_vsopts.HttpOptions{}
	err := packed.UnmarshalTo(options)
	if err != nil {
		return resourceFields{}, fmt.Errorf("%s: %w", optionsPath, err)
	}

	var fields []secretField
	fields = appendHeaderOptions(fields, optionsPath+".request_headers_to_add", options.GetRequestHeadersToAdd())
	fields = appendHeaderOptions(fields, optionsPath+".response_headers_to_add", options.GetResponseHeadersToAdd())
	return resourceFields{fields: fields, options: options, packed: packed}, nil
}

func appendHeaderOptions(fields []secretField, path string, headerOptions []*core.HeaderValueOption) []secretField {
	for i, headerOption := range headerOptions {
		if headerOption.GetHeader() != nil {
			fields = append(fields, secretField{path: fmt.Sprintf("%s[%d].header", path, i), header: headerOption.GetHeader()})
		}
	}
	return fields
}

func metadataPath(key string) string {
	return fmt.Sprintf("metadata[%q]", key)
}

func metadataKeyOf(path string) (key string, isMetadata bool) {
	quoted, isMetadata := strings.CutPrefix(path, "metadata[")
	if !isMetadata {
		return "", false
	}
	key, err := strconv.Unquote(strings.TrimSuffix(quoted, "]"))
	return key, err == nil
}

func refsByPath(refs []FoundRef) map[string]Ref {
	byPath := make(map[string]Ref, len(refs))
	for _, found := range refs {
		byPath[found.Path] = found.Ref
	}
	return byPath
}

// FindRefs gives the secret references of a resource sorted by path, those of its top-level metadata and those of its
// typed fields. The error names the first malformed metadata reference in key order, the well-formed references are
// returned with it. A secret holds none, nor does a nil resource, which stands for one not there yet or gone.
func FindRefs(m proto.Message) ([]FoundRef, error) {
	if _, isSecret := m.(*Secret); isSecret || m == nil {
		return nil, nil
	}
	refs := make([]FoundRef, 0)
	var firstErr error
	metadata := helpers.GetMetadata(m)
	for _, key := range slices.Sorted(maps.Keys(metadata)) {
		ref, isRef, err := ParseRef(metadata[key])
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("metadata key %q: %w", key, err)
		}
		if isRef && err == nil {
			refs = append(refs, FoundRef{Path: metadataPath(key), Ref: ref})
		}
	}

	resource, err := secretFieldsOf(m)
	if err != nil {
		return refs, err
	}
	for _, field := range resource.fields {
		keyRef := field.keyRef()
		if keyRef != nil {
			refs = append(refs, FoundRef{Path: field.path, Ref: Ref{SecretUUID: keyRef.GetSecretUuid(), Key: keyRef.GetKey()}})
		}
	}
	slices.SortFunc(refs, func(a, b FoundRef) int { return strings.Compare(a.Path, b.Path) })
	return refs, firstErr
}

// EachKeyRef calls edit on every typed reference of m, which it may change in place.
func EachKeyRef(m proto.Message, edit func(path string, keyRef *flocktype.SecretKeyRef) error) error {
	resource, err := secretFieldsOf(m)
	if err != nil {
		return err
	}
	edited := false
	for _, field := range resource.fields {
		keyRef := field.keyRef()
		if keyRef == nil {
			continue
		}
		err = edit(field.path, keyRef)
		if err != nil {
			return err
		}
		edited = true
	}
	if !edited {
		return nil
	}
	return resource.packOptions()
}

// Resolve writes into m the values its references point at, valueOf telling whether the secret is held. A metadata
// reference to a secret not held stays as it came, a typed field gets an empty value until the secret arrives.
func Resolve(m proto.Message, refs []FoundRef, valueOf func(Ref) (value string, held bool)) {
	byPath := refsByPath(refs)
	metadata, canWriteMetadata := m.(helpers.MetadataMutable)
	for path, ref := range byPath {
		key, isMetadata := metadataKeyOf(path)
		if !isMetadata || !canWriteMetadata {
			continue
		}
		if value, held := valueOf(ref); held {
			metadata.AddMetadata(key, value)
		}
	}

	resource, err := secretFieldsOf(m)
	if err != nil {
		return
	}
	edited := false
	for _, field := range resource.fields {
		ref, isRef := byPath[field.path]
		if !isRef {
			continue
		}
		value, _ := valueOf(ref)
		field.setValue(value)
		edited = true
	}
	if edited {
		_ = resource.packOptions()
	}
}

// Unresolve puts the references back in place of the values Resolve wrote.
func Unresolve(m proto.Message, refs []FoundRef) {
	byPath := refsByPath(refs)
	metadata, canWriteMetadata := m.(helpers.MetadataMutable)
	for path, ref := range byPath {
		key, isMetadata := metadataKeyOf(path)
		if isMetadata && canWriteMetadata {
			metadata.AddMetadata(key, ref.String())
		}
	}

	resource, err := secretFieldsOf(m)
	if err != nil {
		return
	}
	edited := false
	for _, field := range resource.fields {
		ref, isRef := byPath[field.path]
		if !isRef {
			continue
		}
		field.setKeyRef(&flocktype.SecretKeyRef{SecretUuid: ref.SecretUUID, Key: ref.Key})
		edited = true
	}
	if edited {
		_ = resource.packOptions()
	}
}
