package cli

import (
	"errors"
	"fmt"

	"buf.build/go/protovalidate"
	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	// the extensions are registered so typed_options of a manifest can be unpacked
	_ "github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	_ "github.com/seagalia/flock/sdk/flock/extensions/backend/f5_options/v1"
	_ "github.com/seagalia/flock/sdk/flock/extensions/backend/starling_options/v1"
	_ "github.com/seagalia/flock/sdk/flock/extensions/common/starling_options/v1"
	_ "github.com/seagalia/flock/sdk/flock/extensions/limit/starling_limit/v1"
	_ "github.com/seagalia/flock/sdk/flock/extensions/route/starling_route/v1"
	_ "github.com/seagalia/flock/sdk/flock/extensions/vs/f5_options/v1"
	_ "github.com/seagalia/flock/sdk/flock/extensions/vs/starling_options/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
)

// resourceMessages gives the message of each resource a manifest can describe, by the name the commands use.
var resourceMessages = map[string]func() proto.Message{
	"certificate": func() proto.Message { return &cert.Certificate{} },
	"backend":     func() proto.Message { return &backend.Backend{} },
	"instance":    func() proto.Message { return &instance.Instance{} },
	"secret":      func() proto.Message { return &secret.Secret{} },
	"vip":         func() proto.Message { return &vip.VIP{} },
	"vs":          func() proto.Message { return &vs.VirtualServer{} },
	"zone":        func() proto.Message { return &zone.Zone{} },
}

type Validate struct {
	Resource string `arg:"" enum:"certificate,backend,instance,secret,vip,vs,zone" help:"Resource described by the file: certificate, backend, instance, secret, vip, vs or zone"`
	File     string `short:"f" name:"file" help:"Path to a json or yml file containing the resource (default <resource>.yml)" type:"path" predictor:"file"`
}

type violation struct {
	Field   string
	Message string
}

func (c *Validate) Run() error {
	file := c.File
	if file == "" {
		file = c.Resource + ".yml"
	}
	resource := resourceMessages[c.Resource]()
	loaded, err := fileToMessage(file, resource)
	if err != nil {
		return err
	}
	if !loaded {
		return fmt.Errorf("file %s is empty or missing", msg.Cyan(file))
	}

	violations := validateResource(resource, "")
	if len(violations) == 0 {
		msg.Successf("%s %s is valid", c.Resource, msg.Cyan(file))
		return nil
	}
	for _, v := range violations {
		fmt.Printf("%s: %s\n", v.Field, v.Message)
	}
	return fmt.Errorf("%s %s is not valid: %d violation(s)", c.Resource, file, len(violations))
}

// validateResource runs the protovalidate rules on resource and on every typed_options packed in it:
// protovalidate doesn't look inside an Any, and before this only the agent unpacking it found the errors.
func validateResource(resource proto.Message, path string) []violation {
	var violations []violation
	err := helpers.Validate(resource)
	var validationErr *protovalidate.ValidationError
	switch {
	case errors.As(err, &validationErr):
		for _, v := range validationErr.Violations {
			field := joinFieldPath(path, protovalidate.FieldPathString(v.Proto.GetField()))
			violations = append(violations, violation{Field: field, Message: v.Proto.GetMessage()})
		}
	case err != nil:
		violations = append(violations, violation{Field: path, Message: err.Error()})
	}

	for _, packed := range findPackedOptions(resource.ProtoReflect(), path) {
		options, err := packed.value.UnmarshalNew()
		if err != nil {
			violations = append(violations, violation{Field: packed.path, Message: err.Error()})
			continue
		}
		helpers.ProvDefault(options)
		violations = append(violations, validateResource(options, packed.path)...)
	}
	return violations
}

type packedOption struct {
	path  string
	value *anypb.Any
}

// findPackedOptions returns the Any messages set in m and in its sub messages, with their field path written
// like protovalidate writes it.
func findPackedOptions(m protoreflect.Message, path string) []packedOption {
	if packed, isAny := m.Interface().(*anypb.Any); isAny {
		return []packedOption{{path: path, value: packed}}
	}
	var found []packedOption
	m.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		fieldPath := joinFieldPath(path, string(field.Name()))
		switch {
		case field.IsMap():
			if field.MapValue().Message() == nil {
				return true
			}
			value.Map().Range(func(key protoreflect.MapKey, item protoreflect.Value) bool {
				found = append(found, findPackedOptions(item.Message(), fmt.Sprintf("%s[%q]", fieldPath, key.String()))...)
				return true
			})
		case field.Message() == nil:
			return true
		case field.IsList():
			list := value.List()
			for i := range list.Len() {
				found = append(found, findPackedOptions(list.Get(i).Message(), fmt.Sprintf("%s[%d]", fieldPath, i))...)
			}
		default:
			found = append(found, findPackedOptions(value.Message(), fieldPath)...)
		}
		return true
	})
	return found
}

func joinFieldPath(parent, child string) string {
	if parent == "" {
		return child
	}
	if child == "" {
		return parent
	}
	return parent + "." + child
}
