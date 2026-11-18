// protoc-gen-crd writes the spec types of the Kubernetes provisioner's CRDs and their conversion to the flock protos,
// following the Kubernetes API conventions. It is written for the flock protos only: a construct it doesn't know stops
// the generation instead of vanishing from the CRDs. It reads the ProvDefault of the sdk, so it runs after
// protoc-gen-go wrote it, from its own buf template (api/buf.gen.crd.yaml).
package main

import (
	"flag"
	"fmt"
	"strings"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/seagalia/flock/sdk/helpers"

	// the packages whose messages have a ProvDefault, linked so their types are in the global registry
	_ "github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	_ "github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	_ "github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	_ "github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	_ "github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	_ "github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	_ "github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	_ "github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
)

const (
	specFileFormat    = "zz_generated.%s.spec.go"
	convertFileFormat = "zz_generated.%s.convert.go"
)

func main() {
	var roots []protoreflect.FullName
	var flags flag.FlagSet
	flags.Func("roots", "full names of the messages a kind is made of, separated by ;", func(value string) error {
		for root := range strings.SplitSeq(value, ";") {
			roots = append(roots, protoreflect.FullName(strings.TrimSpace(root)))
		}
		return nil
	})
	protogen.Options{ParamFunc: flags.Set}.Run(func(gen *protogen.Plugin) error {
		return generate(gen, roots, defaultedFields())
	})
}

func generate(gen *protogen.Plugin, roots []protoreflect.FullName, defaulted map[protoreflect.FullName]bool) error {
	if len(roots) == 0 {
		return fmt.Errorf("no roots: set the roots option")
	}
	b := newBuilder(gen.Files, defaulted)
	for _, root := range roots {
		b.addRoot(root)
	}
	if err := b.err(); err != nil {
		return err
	}
	for _, group := range b.fileGroups() {
		spec, err := renderSpec(group)
		if err != nil {
			return err
		}
		convert, err := renderConvert(group)
		if err != nil {
			return err
		}
		// the files belong to no proto file, the import path only matters to protogen's own import handling
		gen.NewGeneratedFile(fmt.Sprintf(specFileFormat, group.name), "").P(string(spec))
		gen.NewGeneratedFile(fmt.Sprintf(convertFileFormat, group.name), "").P(string(convert))
	}
	return nil
}

// defaultedFields are the fields ProvDefault, the defaults the controller gives every received message, sets on an
// empty message. They stay optional in the CRD whatever their rules: an absent field takes the default. A message
// default the rules refuse doesn't count (Backend.health_check gets a HealthCheck with no checker), the controller
// refuses the absent field anyway.
func defaultedFields() map[protoreflect.FullName]bool {
	defaulted := map[protoreflect.FullName]bool{}
	protoregistry.GlobalTypes.RangeMessages(func(messageType protoreflect.MessageType) bool {
		if !strings.HasPrefix(string(messageType.Descriptor().FullName()), "flock.") {
			return true
		}
		message := messageType.New()
		helpers.ProvDefault(message.Interface())
		message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
			isMessage := field.Message() != nil && !field.IsList() && !field.IsMap()
			if isMessage && helpers.Validate(value.Message().Interface()) != nil {
				return true
			}
			defaulted[field.FullName()] = true
			return true
		})
		return true
	})
	return defaulted
}
