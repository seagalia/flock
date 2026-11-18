package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"github.com/jhump/protoreflect/desc" //nolint:staticcheck // the protoset package gives jhump/protoreflect descriptors
	"github.com/seagalia/flock/cli/protoset"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

type Explain struct {
	Target string `arg:"" optional:"" help:"Resource (certificate, backend, instance, route, secret, vip, vs, zone) or full name of a message, e.g. flock.extensions.vs.starling_options.v1.HttpOptions. Lists them when omitted."`
	Path   string `arg:"" optional:"" help:"Dot path to a field of the message to explain instead, e.g. http_routes_config.routes"`
	Json   bool   `short:"j" name:"json" help:"Print the explanation in JSON"`
}

type messageExplanation struct {
	Name    string             `json:"name"`
	Comment string             `json:"comment,omitempty"`
	Fields  []fieldExplanation `json:"fields"`
}

type fieldExplanation struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	Oneof      string   `json:"oneof,omitempty"`
	EnumValues []string `json:"enum_values,omitempty"`
	Comment    string   `json:"comment,omitempty"`
	Rules      string   `json:"rules,omitempty"`
	// Next is the command explaining the message type of the field, when it has one.
	Next string `json:"next,omitempty"`
}

func (c *Explain) Run() error {
	if c.Target == "" {
		return printExplainTargets()
	}
	message, err := c.findMessage()
	if err != nil {
		return err
	}
	explanation := c.explainMessage(message)
	if c.Json {
		data, err := json.MarshalIndent(explanation, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}
	printMessageExplanation(explanation)
	return nil
}

// findMessage returns the message of the target, or of the field at the end of the path.
func (c *Explain) findMessage() (*desc.MessageDescriptor, error) {
	messageName := c.Target
	if newResource, isResource := resourceMessages[c.Target]; isResource {
		messageName = string(newResource().ProtoReflect().Descriptor().FullName())
	}
	message, err := protoset.FindMessage(messageName)
	if err != nil {
		return nil, err
	}
	if c.Path == "" {
		return message, nil
	}
	for fieldName := range strings.SplitSeq(c.Path, ".") {
		field := message.FindFieldByName(fieldName)
		if field == nil {
			return nil, fmt.Errorf("%s has no field %s, fields are: %s", message.GetFullyQualifiedName(), fieldName, strings.Join(fieldNames(message), ", "))
		}
		fieldMessage := valueMessage(field)
		if fieldMessage == nil {
			return nil, fmt.Errorf("field %s of %s is a %s, not a message", fieldName, message.GetFullyQualifiedName(), fieldType(field))
		}
		message = fieldMessage
	}
	return message, nil
}

func (c *Explain) explainMessage(message *desc.MessageDescriptor) messageExplanation {
	explanation := messageExplanation{
		Name:    message.GetFullyQualifiedName(),
		Comment: commentText(message.GetSourceInfo().GetLeadingComments()),
	}
	for _, field := range message.GetFields() {
		explained := fieldExplanation{
			Name:    field.GetName(),
			Type:    fieldType(field),
			Comment: commentText(field.GetSourceInfo().GetLeadingComments()),
			Rules:   validationRules(field.GetFieldOptions()),
		}
		if field.GetOneOf() != nil {
			explained.Oneof = oneofDescription(field.GetOneOf())
		}
		if field.GetEnumType() != nil {
			for _, value := range field.GetEnumType().GetValues() {
				explained.EnumValues = append(explained.EnumValues, value.GetName())
			}
		}
		explained.Next = c.nextCommand(field)
		explanation.Fields = append(explanation.Fields, explained)
	}
	return explanation
}

// nextCommand tells how to explain the message type of field: a drill down for a sub message, the
// extension itself for a typed_options Any.
func (c *Explain) nextCommand(field *desc.FieldDescriptor) string {
	fieldMessage := valueMessage(field)
	if fieldMessage == nil {
		return ""
	}
	if fieldMessage.GetFullyQualifiedName() == "google.protobuf.Any" {
		return `pack an extension listed in the comment with "@type": type.googleapis.com/<extension>, explained by flock explain <extension>`
	}
	if strings.HasPrefix(fieldMessage.GetFullyQualifiedName(), "google.protobuf.") {
		return ""
	}
	fieldPath := joinFieldPath(c.Path, field.GetName())
	return fmt.Sprintf("flock explain %s %s", c.Target, fieldPath)
}

// valueMessage returns the message held by field: its type, or the type of its values for a map. It is
// nil for scalars and enums.
func valueMessage(field *desc.FieldDescriptor) *desc.MessageDescriptor {
	if field.IsMap() {
		return field.GetMapValueType().GetMessageType()
	}
	return field.GetMessageType()
}

func fieldType(field *desc.FieldDescriptor) string {
	if field.IsMap() {
		return fmt.Sprintf("map<%s, %s>", fieldType(field.GetMapKeyType()), fieldType(field.GetMapValueType()))
	}
	var typeName string
	switch {
	case field.GetMessageType() != nil:
		typeName = field.GetMessageType().GetFullyQualifiedName()
	case field.GetEnumType() != nil:
		typeName = field.GetEnumType().GetFullyQualifiedName()
	default:
		typeName = strings.ToLower(strings.TrimPrefix(field.GetType().String(), "TYPE_"))
	}
	if field.IsRepeated() {
		return "repeated " + typeName
	}
	return typeName
}

func oneofDescription(oneof *desc.OneOfDescriptor) string {
	rules, _ := proto.GetExtension(oneof.GetOneOfOptions(), validate.E_Oneof).(*validate.OneofRules)
	if rules.GetRequired() {
		return oneof.GetName() + " (set exactly one)"
	}
	return oneof.GetName() + " (set at most one)"
}

// validationRules returns the protovalidate rules of a field as compact json, empty when it has none.
func validationRules(options *descriptorpb.FieldOptions) string {
	rules, _ := proto.GetExtension(options, validate.E_Field).(*validate.FieldRules)
	if rules == nil {
		return ""
	}
	data, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(rules)
	if err != nil {
		return ""
	}
	// protojson adds spaces at random on purpose to keep callers from relying on its output, json.Compact makes it
	// stable
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, data); err != nil {
		return ""
	}
	return compacted.String()
}

// commentText removes the space protoc keeps at the start of each line of a comment.
func commentText(comment string) string {
	lines := strings.Split(strings.TrimSpace(comment), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return strings.Join(lines, "\n")
}

func fieldNames(message *desc.MessageDescriptor) []string {
	names := make([]string, 0, len(message.GetFields()))
	for _, field := range message.GetFields() {
		names = append(names, field.GetName())
	}
	return names
}

func printMessageExplanation(explanation messageExplanation) {
	fmt.Println(explanation.Name)
	printIndented(explanation.Comment, "  ")
	for _, field := range explanation.Fields {
		fmt.Println()
		fmt.Printf("%s: %s\n", field.Name, field.Type)
		printIndented(field.Comment, "    ")
		if field.Oneof != "" {
			fmt.Printf("    oneof: %s\n", field.Oneof)
		}
		if len(field.EnumValues) > 0 {
			fmt.Printf("    values: %s\n", strings.Join(field.EnumValues, ", "))
		}
		if field.Rules != "" {
			fmt.Printf("    rules: %s\n", field.Rules)
		}
		if field.Next != "" {
			fmt.Printf("    → %s\n", field.Next)
		}
	}
}

func printIndented(text, indent string) {
	if text == "" {
		return
	}
	for line := range strings.SplitSeq(text, "\n") {
		fmt.Println(indent + line)
	}
}

func printExplainTargets() error {
	extensions, err := protoset.ExtensionMessages()
	if err != nil {
		return err
	}
	resources := make([]string, 0, len(resourceMessages))
	for resource := range resourceMessages {
		resources = append(resources, resource)
	}
	slices.Sort(resources)

	fmt.Println("Resources (flock explain <resource> [field.path]):")
	for _, resource := range resources {
		fmt.Printf("  %-12s %s\n", resource, resourceMessages[resource]().ProtoReflect().Descriptor().FullName())
	}
	fmt.Println()
	fmt.Println("Extensions, packed in typed_options fields (flock explain <extension>):")
	for _, extension := range extensions {
		fmt.Println("  " + extension)
	}
	return nil
}
