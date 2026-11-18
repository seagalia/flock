package protoset

import (
	_ "embed"
	"fmt"
	"slices"
	"strings"

	"github.com/fullstorydev/grpcurl"
	"github.com/jhump/protoreflect/desc" //nolint:staticcheck // grpcurl reads the protoset into jhump/protoreflect descriptors
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

//go:embed protoset.bin
var protosetContent []byte

func GetAllInfo() ([]*desc.MethodDescriptor, []*desc.FileDescriptor, error) {
	fileSource, err := loadSource()
	if err != nil {
		return nil, nil, err
	}

	allMethods, err := getMethods(fileSource)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get all methods: %w", err)
	}
	allFiles, err := grpcurl.GetAllFiles(fileSource)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get all files: %w", err)
	}
	return allMethods, allFiles, nil
}

// FindMessage returns the descriptor, with its comments, of the message named fullName (e.g. flock.api.config.vs.v1.VirtualServer).
func FindMessage(fullName string) (*desc.MessageDescriptor, error) {
	fileSource, err := loadSource()
	if err != nil {
		return nil, err
	}
	d, err := fileSource.FindSymbol(fullName)
	if err != nil {
		return nil, fmt.Errorf("unknown message %s: %w", fullName, err)
	}
	message, ok := d.(*desc.MessageDescriptor)
	if !ok {
		return nil, fmt.Errorf("%s is a %T, not a message", fullName, d)
	}
	return message, nil
}

// ExtensionMessages returns the full names of the messages that can be packed in a typed_options field, sorted.
func ExtensionMessages() ([]string, error) {
	fileSource, err := loadSource()
	if err != nil {
		return nil, err
	}
	files, err := grpcurl.GetAllFiles(fileSource)
	if err != nil {
		return nil, fmt.Errorf("failed to get all files: %w", err)
	}
	var names []string
	for _, file := range files {
		if !strings.HasPrefix(file.GetPackage(), "flock.extensions.") {
			continue
		}
		for _, message := range file.GetMessageTypes() {
			names = append(names, message.GetFullyQualifiedName())
		}
	}
	slices.Sort(names)
	return names, nil
}

func loadSource() (grpcurl.DescriptorSource, error) {
	fs := &descriptorpb.FileDescriptorSet{}
	err := proto.Unmarshal(protosetContent, fs)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal protoset: %w", err)
	}
	fileSource, err := grpcurl.DescriptorSourceFromFileDescriptorSet(fs)
	if err != nil {
		return nil, fmt.Errorf("failed to create file descriptor source: %w", err)
	}
	return fileSource, nil
}

func getMethods(source grpcurl.DescriptorSource) ([]*desc.MethodDescriptor, error) {
	allServices, err := source.ListServices()
	if err != nil {
		return nil, err
	}

	var descs []*desc.MethodDescriptor
	for _, svc := range allServices {
		if svc == "grpc.reflection.v1alpha.ServerReflection" {
			continue
		}
		d, err := source.FindSymbol(svc)
		if err != nil {
			return nil, err
		}
		sd, ok := d.(*desc.ServiceDescriptor)
		if !ok {
			return nil, fmt.Errorf("%s should be a service descriptor but instead is a %T", d.GetFullyQualifiedName(), d)
		}
		descs = append(descs, sd.GetMethods()...)
	}
	return descs, nil
}
