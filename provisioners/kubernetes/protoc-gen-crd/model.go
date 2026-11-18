package main

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const (
	extensionsPackagePrefix = "flock.extensions."
	unspecifiedSuffix       = "UNSPECIFIED"
)

// extensionPattern finds the extensions an Any field accepts in its comment, the same way webui/protodocs does.
var extensionPattern = regexp.MustCompile(`flock\.extensions\.[A-Za-z0-9_.]+[A-Za-z0-9_]`)

type shape int

const (
	shapeScalar shape = iota
	shapeBool
	shapeBytes
	shapeEnum
	shapeMessage
	shapeDuration
	shapeWrapper
	shapeStruct
	shapeAny
	shapeReference
	shapeMap
)

type celRule struct {
	rule    string
	message string
}

// goType is a Go struct of the CRDs: a proto message, or the wrapper standing for an Any field.
type goType struct {
	name string
	// level is the depth of a message holding itself, 1 for every other message
	level    int
	doc      string
	message  *protogen.Message
	anyField *protogen.Field
	fields   []*goField
	celRules []celRule
}

// goField is a field of a goType and how it converts to its proto field.
type goField struct {
	source   *protogen.Field
	goName   string
	jsonName string
	doc      string
	shape    shape
	pointer  bool
	list     bool
	elem     protogen.GoIdent // the element type, a GoIdent with no import path for the generated and builtin types
	enum     *goEnum
	kind     string // the kind a reference names
	tag      string
	markers  []string
	celRules []celRule
}

type messageKey struct {
	name  protoreflect.FullName
	level int
}

type goEnum struct {
	name   string
	enum   *protogen.Enum
	values []goEnumValue
}

type goEnumValue struct {
	constName string
	value     string
	proto     *protogen.EnumValue
}

// builder turns the messages reachable from the roots into goTypes, collecting every construct it can't express.
type builder struct {
	messages  map[protoreflect.FullName]*protogen.Message
	defaulted map[protoreflect.FullName]bool
	roots     map[protoreflect.FullName]bool
	rootTypes []*goType
	types     []*goType
	byMessage map[messageKey]*goType
	building  map[protoreflect.FullName]bool
	enums     []*goEnum
	byEnum    map[protoreflect.FullName]*goEnum
	owners    map[string]string
	errs      []error
}

func newBuilder(files []*protogen.File, defaulted map[protoreflect.FullName]bool) *builder {
	b := &builder{
		messages:  map[protoreflect.FullName]*protogen.Message{},
		defaulted: defaulted,
		roots:     map[protoreflect.FullName]bool{},
		byMessage: map[messageKey]*goType{},
		building:  map[protoreflect.FullName]bool{},
		byEnum:    map[protoreflect.FullName]*goEnum{},
		owners:    map[string]string{},
	}
	for _, file := range files {
		b.indexMessages(file.Messages)
	}
	return b
}

func (b *builder) indexMessages(messages []*protogen.Message) {
	for _, message := range messages {
		b.messages[message.Desc.FullName()] = message
		b.indexMessages(message.Messages)
	}
}

func (b *builder) fail(subject protoreflect.FullName, format string, args ...any) {
	b.errs = append(b.errs, fmt.Errorf("%s: %s", subject, fmt.Sprintf(format, args...)))
}

func (b *builder) err() error {
	return errors.Join(b.errs...)
}

func (b *builder) addRoot(fullName protoreflect.FullName) {
	message, found := b.messages[fullName]
	if !found {
		b.fail(fullName, "root message not found")
		return
	}
	b.roots[fullName] = true
	b.rootTypes = append(b.rootTypes, b.addMessage(message, 1))
}

func (b *builder) addMessage(message *protogen.Message, level int) *goType {
	fullName := message.Desc.FullName()
	key := messageKey{name: fullName, level: level}
	if known, found := b.byMessage[key]; found {
		return known
	}
	t := &goType{message: message, level: level, doc: comment(message.Comments.Leading)}
	t.name = b.messageTypeName(message)
	if level > 1 {
		t.name += fmt.Sprintf("Level%d", level)
	}
	b.claimName(fullName, t.name)
	b.byMessage[key] = t
	b.types = append(b.types, t)
	b.building[fullName] = true
	defer delete(b.building, fullName)

	drops := b.droppedFields(message)
	for _, field := range message.Fields {
		if slices.Contains(drops, field.Desc.Name()) {
			continue
		}
		if built := b.field(t, field); built != nil {
			t.fields = append(t.fields, built)
		}
	}
	for _, ref := range overrides[fullName].refs {
		t.fields = append(t.fields, &goField{
			goName:   typeName(upperFirst(ref.jsonName)),
			jsonName: ref.jsonName,
			doc:      ref.doc,
			shape:    shapeReference,
			kind:     ref.kind,
			elem:     protogen.GoIdent{GoName: "ObjectRef"},
			tag:      ",omitzero",
			markers:  []string{"required"},
		})
	}
	b.addOneofRules(t, drops)
	b.addMessageRules(t)
	return t
}

func (b *builder) droppedFields(message *protogen.Message) []protoreflect.Name {
	var drops []protoreflect.Name
	if b.roots[message.Desc.FullName()] {
		drops = append(drops, rootDrops...)
	}
	messageOverride, found := overrides[message.Desc.FullName()]
	if !found {
		return drops
	}
	for _, name := range messageOverride.drop {
		if message.Desc.Fields().ByName(name) == nil {
			b.fail(message.Desc.FullName(), "override drops %s, which the message doesn't have", name)
		}
	}
	return append(drops, messageOverride.drop...)
}

// messageTypeName names a message after its path in its package (`URL.UserInfo` gives `URLUserInfo`), roots taking
// the Spec suffix their kind expects and extension messages the name of their agent (`StarlingBackendOptions`).
func (b *builder) messageTypeName(message *protogen.Message) string {
	fullName := message.Desc.FullName()
	if renamed, found := typeRenames[fullName]; found {
		return renamed
	}
	name := scopedTypeName(message.Desc.ParentFile().Package(), fullName)
	if b.roots[fullName] {
		return name + "Spec"
	}
	return name
}

func scopedTypeName(pkg protoreflect.FullName, fullName protoreflect.FullName) string {
	var name strings.Builder
	relative := strings.TrimPrefix(string(fullName), string(pkg)+".")
	for part := range strings.SplitSeq(relative, ".") {
		name.WriteString(typeName(part))
	}
	agent := extensionAgent(pkg)
	isNamedAfterAgent := agent != "" && strings.HasPrefix(name.String(), goWord(agent))
	if agent == "" || isNamedAfterAgent {
		return name.String()
	}
	return goWord(agent) + name.String()
}

// extensionAgent is the agent of an extension package, `starling` for `flock.extensions.backend.starling_options.v1`.
func extensionAgent(pkg protoreflect.FullName) string {
	if !strings.HasPrefix(string(pkg), extensionsPackagePrefix) {
		return ""
	}
	parts := strings.Split(string(pkg), ".")
	if len(parts) < 4 {
		return ""
	}
	agent, _, _ := strings.Cut(parts[3], "_")
	return agent
}

func (b *builder) claimName(owner protoreflect.FullName, name string) string {
	if previous, taken := b.owners[name]; taken && previous != string(owner) {
		b.fail(owner, "Go name %s is already the name of %s, add one of them to typeRenames", name, previous)
	}
	b.owners[name] = string(owner)
	return name
}

func (b *builder) field(owner *goType, field *protogen.Field) *goField {
	rules := fieldRules(field)
	b.checkFieldRules(field)
	goName, jsonName := fieldNames(string(field.Desc.Name()))
	built := &goField{source: field, goName: goName, jsonName: jsonName, doc: comment(field.Comments.Leading)}
	isOneofMember := field.Oneof != nil && !field.Oneof.Desc.IsSynthetic()
	if field.Desc.HasOptionalKeyword() {
		b.fail(field.Desc.FullName(), "proto3 optional fields are not supported")
		return nil
	}
	depth := recursionDepths[owner.message.Desc.FullName()]
	isLastLevel := field.Message == owner.message && owner.level == depth
	if isLastLevel {
		owner.doc += fmt.Sprintf("\n\nThe CRD stops %s at this depth (%d), since a schema can't hold its own type.",
			jsonName, depth)
		return nil
	}

	var c constraints
	switch {
	case isReference(field):
		c = b.referenceField(built, rules)
	case field.Desc.IsMap():
		c = b.mapField(built, rules)
	case field.Desc.IsList():
		c = b.listField(owner, built, rules)
	case field.Message != nil:
		c = b.messageField(owner, built, rules)
	case field.Enum != nil:
		c = b.enumField(built, rules)
	default:
		c = b.scalarField(built, rules)
	}
	b.addFieldCelRules(built, rules, &c)

	isDefaulted := b.defaulted[field.Desc.FullName()]
	// the API refuses an absent field whose zero value breaks a rule, unless ProvDefault fills it first
	refusedWhenAbsent := rules.GetRequired() || !c.zeroValid
	required := !isOneofMember && !isDefaulted && refusedWhenAbsent
	// a oneof member needs has() to tell it is set, which bytes already gets from nil
	isOneofScalar := isOneofMember && built.shape != shapeBytes
	hasPresence := built.shape == shapeDuration || built.shape == shapeWrapper
	zeroIsAChoice := hasPresence && c.zeroValid
	built.pointer = built.pointer || isOneofScalar || built.shape == shapeBool || zeroIsAChoice
	if built.shape == shapeMessage && !built.list && !required {
		built.pointer = true
	}
	if !c.bounded {
		b.fail(field.Desc.FullName(), "no maximum: add max_len, max_bytes, max_items or max_pairs to the proto")
	}

	built.markers = append(built.markers, c.markers...)
	built.celRules = append(built.celRules, c.celRules...)
	b.setRequiredness(built, required)
	return built
}

func (b *builder) setRequiredness(field *goField, required bool) {
	isValueStruct := (field.shape == shapeMessage || field.shape == shapeReference) && !field.pointer && !field.list
	switch {
	case required && isValueStruct:
		field.tag = ",omitzero"
		field.markers = append([]string{"required"}, field.markers...)
	case required && !field.pointer && !field.list && field.shape != shapeMap:
		field.markers = append([]string{"required"}, field.markers...)
	case required:
		field.tag = ",omitempty"
		field.markers = append([]string{"required"}, field.markers...)
	default:
		field.tag = ",omitempty"
		field.markers = append([]string{"optional"}, field.markers...)
	}
}

func isReference(field *protogen.Field) bool {
	return field.Desc.Kind() == protoreflect.StringKind && strings.HasSuffix(string(field.Desc.Name()), "_uuid")
}

func (b *builder) referenceField(built *goField, rules *validate.FieldRules) constraints {
	target, found := references[built.source.Desc.Name()]
	if !found {
		b.fail(built.source.Desc.FullName(), "uuid field missing from the references table")
		return constraints{bounded: true}
	}
	built.shape = shapeReference
	built.kind = target.kind
	built.jsonName = target.jsonName
	built.goName = typeName(upperFirst(target.jsonName))
	built.elem = protogen.GoIdent{GoName: "ObjectRef"}
	if !built.source.Desc.IsList() {
		return constraints{bounded: true, zeroValid: !hasMinLength(rules.GetString())}
	}
	built.list = true
	c := b.repeatedConstraints(built.source, rules.GetRepeated())
	if rules.GetRepeated().GetUnique() {
		c.markers = replaceListType(c.markers, "listType=map", "listMapKey=name")
	}
	return c
}

func hasMinLength(rules *validate.StringRules) bool {
	return rules.GetMinLen() > 0
}

func (b *builder) mapField(built *goField, rules *validate.FieldRules) constraints {
	field := built.source
	built.shape = shapeMap
	isStringMap := field.Desc.MapKey().Kind() == protoreflect.StringKind && field.Desc.MapValue().Kind() == protoreflect.StringKind
	if !isStringMap {
		b.fail(field.Desc.FullName(), "only map<string, string> is supported, the conventions want lists of named subobjects")
	}
	return b.mapConstraints(field, rules.GetMap())
}

func (b *builder) listField(owner *goType, built *goField, rules *validate.FieldRules) constraints {
	field := built.source
	built.list = true
	c := b.repeatedConstraints(field, rules.GetRepeated())
	switch {
	case field.Message != nil:
		built.shape = shapeMessage
		built.elem = protogen.GoIdent{GoName: b.messageElem(owner, field)}
	case field.Enum != nil:
		built.shape = shapeEnum
		built.enum = b.addEnum(field.Enum)
		built.elem = protogen.GoIdent{GoName: built.enum.name}
	default:
		built.shape = shapeScalar
		built.elem = protogen.GoIdent{GoName: b.scalarGoType(field)}
	}
	return c
}

func (b *builder) messageElem(owner *goType, field *protogen.Field) string {
	if isWellKnown(field.Message) {
		b.fail(field.Desc.FullName(), "lists of %s are not supported", field.Message.Desc.FullName())
		return "any"
	}
	return b.messageType(owner, field).name
}

// messageType is the type of the message a field of owner holds. A message holding itself gets one type per level,
// down to its depth in recursionDepths, since a CRD schema can't refer to itself.
func (b *builder) messageType(owner *goType, field *protogen.Field) *goType {
	message := field.Message
	isSelf := owner.message == message
	_, hasDepth := recursionDepths[message.Desc.FullName()]
	if isSelf && hasDepth {
		return b.addMessage(message, owner.level+1)
	}
	if b.building[message.Desc.FullName()] {
		b.fail(field.Desc.FullName(), "%s holds itself, add it to recursionDepths", message.Desc.FullName())
		return owner
	}
	return b.addMessage(message, 1)
}

func isWellKnown(message *protogen.Message) bool {
	return message.Desc.ParentFile().Package() == "google.protobuf"
}

func (b *builder) messageField(owner *goType, built *goField, rules *validate.FieldRules) constraints {
	field := built.source
	switch field.Message.Desc.FullName() {
	case "google.protobuf.Duration":
		built.shape = shapeDuration
		built.goName += "Milliseconds"
		built.jsonName += "Milliseconds"
		built.elem = protogen.GoIdent{GoName: "int32"}
		return b.durationConstraints(field, rules.GetDuration())
	case "google.protobuf.Any":
		built.shape = shapeAny
		built.pointer = true
		built.elem = protogen.GoIdent{GoName: b.addAnyWrapper(owner, field).name}
		return constraints{bounded: true, zeroValid: true}
	case "google.protobuf.Struct":
		built.shape = shapeStruct
		built.pointer = true
		built.elem = protogen.GoIdent{GoName: "JSON", GoImportPath: apiextensionsPath}
		built.markers = append(built.markers, "kubebuilder:validation:Schemaless", "kubebuilder:validation:Type=object",
			"kubebuilder:pruning:PreserveUnknownFields")
		return constraints{bounded: true, zeroValid: true}
	case "google.protobuf.UInt32Value", "google.protobuf.Int32Value", "google.protobuf.BoolValue",
		"google.protobuf.StringValue":
		return b.wrapperField(built, rules)
	}
	if isWellKnown(field.Message) {
		b.fail(field.Desc.FullName(), "%s is not supported", field.Message.Desc.FullName())
		return constraints{bounded: true}
	}
	built.shape = shapeMessage
	built.elem = protogen.GoIdent{GoName: b.messageType(owner, field).name}
	return constraints{bounded: true, zeroValid: !rules.GetRequired()}
}

// wrapperField reads the rules of a wrapper field as the rules of its value, as protovalidate does.
func (b *builder) wrapperField(built *goField, rules *validate.FieldRules) constraints {
	built.shape = shapeWrapper
	field := built.source
	switch field.Message.Desc.FullName() {
	case "google.protobuf.UInt32Value":
		built.elem = protogen.GoIdent{GoName: "int32"}
		return b.numberConstraints(field, rules.GetUint32(), typeBounds{min: new(int64(0))})
	case "google.protobuf.Int32Value":
		built.elem = protogen.GoIdent{GoName: "int32"}
		return b.numberConstraints(field, rules.GetInt32(), typeBounds{})
	case "google.protobuf.BoolValue":
		built.elem = protogen.GoIdent{GoName: "bool"}
		built.pointer = true
		return constraints{bounded: true, zeroValid: true}
	default:
		built.elem = protogen.GoIdent{GoName: "string"}
		built.pointer = true
		return b.stringConstraints(field, rules.GetString())
	}
}

func (b *builder) enumField(built *goField, rules *validate.FieldRules) constraints {
	built.shape = shapeEnum
	built.enum = b.addEnum(built.source.Enum)
	built.elem = protogen.GoIdent{GoName: built.enum.name}
	return b.enumConstraints(built.source, rules.GetEnum())
}

func (b *builder) scalarField(built *goField, rules *validate.FieldRules) constraints {
	field := built.source
	built.elem = protogen.GoIdent{GoName: b.scalarGoType(field)}
	switch field.Desc.Kind() {
	case protoreflect.BoolKind:
		built.shape = shapeBool
		return constraints{bounded: true, zeroValid: true}
	case protoreflect.BytesKind:
		built.shape = shapeBytes
		return b.bytesConstraints(field, rules.GetBytes())
	case protoreflect.StringKind:
		built.shape = shapeScalar
		return b.stringConstraints(field, rules.GetString())
	case protoreflect.Uint32Kind:
		built.shape = shapeScalar
		return b.numberConstraints(field, rules.GetUint32(), typeBounds{min: new(int64(0))})
	case protoreflect.Uint64Kind:
		built.shape = shapeScalar
		return b.numberConstraints(field, rules.GetUint64(), typeBounds{min: new(int64(0)), max: new(maxSafeInteger)})
	case protoreflect.Int32Kind:
		built.shape = shapeScalar
		return b.numberConstraints(field, rules.GetInt32(), typeBounds{})
	case protoreflect.Int64Kind:
		built.shape = shapeScalar
		return b.numberConstraints(field, rules.GetInt64(),
			typeBounds{min: new(-maxSafeInteger), max: new(maxSafeInteger)})
	}
	return constraints{bounded: true}
}

// scalarGoType is the Go type of a scalar in the CRD: no unsigned ints and no floats, as the conventions ask.
func (b *builder) scalarGoType(field *protogen.Field) string {
	switch field.Desc.Kind() {
	case protoreflect.BoolKind:
		return "bool"
	case protoreflect.BytesKind:
		return "[]byte"
	case protoreflect.StringKind:
		return "string"
	case protoreflect.Uint32Kind, protoreflect.Int32Kind:
		return "int32"
	case protoreflect.Uint64Kind, protoreflect.Int64Kind:
		return "int64"
	}
	b.fail(field.Desc.FullName(), "%s fields are not supported", field.Desc.Kind())
	return "any"
}

// addAnyWrapper writes the struct an Any field becomes: one optional member per extension its comment lists, named
// after the agent of the extension (`starling`, `f5`).
func (b *builder) addAnyWrapper(owner *goType, field *protogen.Field) *goType {
	fieldGoName, _ := fieldNames(string(field.Desc.Name()))
	wrapper := &goType{anyField: field, doc: comment(field.Comments.Leading)}
	wrapper.name = b.claimName(field.Desc.FullName(), strings.TrimSuffix(owner.name, "Spec")+fieldGoName)
	b.types = append(b.types, wrapper)

	extensions := slices.Compact(slices.Sorted(slices.Values(extensionPattern.FindAllString(string(field.Comments.Leading), -1))))
	if len(extensions) == 0 {
		b.fail(field.Desc.FullName(), "Any field whose comment names no flock.extensions message")
	}
	var members []string
	for _, extension := range extensions {
		message, found := b.messages[protoreflect.FullName(extension)]
		if !found {
			b.fail(field.Desc.FullName(), "extension %s not found", extension)
			continue
		}
		agent := extensionAgent(message.Desc.ParentFile().Package())
		if slices.Contains(members, agent) {
			b.fail(field.Desc.FullName(), "two extensions of the agent %s, members need another name", agent)
		}
		members = append(members, agent)
		wrapper.fields = append(wrapper.fields, &goField{
			goName:   goWord(agent),
			jsonName: agent,
			doc:      comment(message.Comments.Leading),
			shape:    shapeMessage,
			pointer:  true,
			elem:     protogen.GoIdent{GoName: b.addMessage(message, 1).name},
			tag:      ",omitempty",
			markers:  []string{"optional"},
		})
	}
	if len(members) > 1 {
		wrapper.celRules = append(wrapper.celRules, atMostOne(members))
	}
	return wrapper
}

func (b *builder) addEnum(enum *protogen.Enum) *goEnum {
	fullName := enum.Desc.FullName()
	if known, found := b.byEnum[fullName]; found {
		return known
	}
	built := &goEnum{enum: enum}
	built.name = b.claimName(fullName, scopedTypeName(enum.Desc.ParentFile().Package(), fullName))
	values := map[string]bool{}
	for _, value := range enum.Values {
		// an unspecified value is the absent field, never written
		if strings.HasSuffix(string(value.Desc.Name()), unspecifiedSuffix) {
			continue
		}
		crdValue := enumValue(string(value.Desc.Name()))
		if values[crdValue] {
			b.fail(value.Desc.FullName(), "two values of %s give %s", fullName, crdValue)
		}
		values[crdValue] = true
		built.values = append(built.values, goEnumValue{constName: built.name + crdValue, value: crdValue, proto: value})
	}
	b.byEnum[fullName] = built
	b.enums = append(b.enums, built)
	return built
}

func (b *builder) addOneofRules(t *goType, drops []protoreflect.Name) {
	for _, oneof := range t.message.Oneofs {
		if oneof.Desc.IsSynthetic() {
			continue
		}
		var members []string
		for _, field := range oneof.Fields {
			if slices.Contains(drops, field.Desc.Name()) {
				continue
			}
			members = append(members, jsonNameOf(t, field))
		}
		isRequired := oneofRules(oneof).GetRequired()
		switch {
		case isRequired && len(members) > 0:
			t.celRules = append(t.celRules, exactlyOne(members))
		case len(members) > 1:
			t.celRules = append(t.celRules, atMostOne(members))
		}
	}
}

func jsonNameOf(t *goType, field *protogen.Field) string {
	for _, built := range t.fields {
		if built.source == field {
			return built.jsonName
		}
	}
	_, jsonName := fieldNames(string(field.Desc.Name()))
	return jsonName
}

func exactlyOne(members []string) celRule {
	return celRule{
		rule:    fmt.Sprintf("[%s].exists_one(x, x)", hasList(members)),
		message: "exactly one of " + strings.Join(members, ", ") + " must be set",
	}
}

func atMostOne(members []string) celRule {
	return celRule{
		rule:    fmt.Sprintf("[%s].filter(x, x).size() <= 1", hasList(members)),
		message: "at most one of " + strings.Join(members, ", ") + " can be set",
	}
}

func hasList(members []string) string {
	checks := make([]string, 0, len(members))
	for _, member := range members {
		checks = append(checks, "has(self."+member+")")
	}
	return strings.Join(checks, ", ")
}

func (b *builder) addMessageRules(t *goType) {
	rules, _ := proto.GetExtension(t.message.Desc.Options(), validate.E_Message).(*validate.MessageRules)
	for _, name := range unsupportedRules(rules, "cel") {
		b.fail(t.message.Desc.FullName(), "message rule %s is not supported", name)
	}
	for _, rule := range rules.GetCel() {
		expression, found := celExpressions[rule.GetId()]
		if !found {
			b.fail(t.message.Desc.FullName(), "CEL rule %q missing from celExpressions", rule.GetId())
			continue
		}
		t.celRules = append(t.celRules, celRule{rule: expression, message: rule.GetMessage()})
	}
}

func (b *builder) addFieldCelRules(built *goField, rules *validate.FieldRules, c *constraints) {
	for _, rule := range rules.GetCel() {
		if rule.GetId() == uniqueNamesRule {
			b.uniqueNames(built, c)
			continue
		}
		expression, found := celExpressions[rule.GetId()]
		if !found {
			b.fail(built.source.Desc.FullName(), "CEL rule %q missing from celExpressions", rule.GetId())
			continue
		}
		c.celRules = append(c.celRules, celRule{rule: expression, message: rule.GetMessage()})
	}
}

// uniqueNames turns the unique_names rule into a list keyed on name, which the apiserver keeps unique and server-side
// apply merges entry by entry. listMapKey refuses an optional key, hence the check on name.
func (b *builder) uniqueNames(built *goField, c *constraints) {
	field := built.source
	if !built.list || field.Message == nil {
		b.fail(field.Desc.FullName(), "%s only applies to lists of messages", uniqueNamesRule)
		return
	}
	name := field.Message.Desc.Fields().ByName("name")
	if name == nil || !hasMinLength(fieldRulesOf(name).GetString()) {
		b.fail(field.Desc.FullName(), "%s needs a name field of at least one character in %s", uniqueNamesRule,
			field.Message.Desc.FullName())
		return
	}
	c.markers = replaceListType(c.markers, "listType=map", "listMapKey=name")
}

func replaceListType(markers []string, listMarkers ...string) []string {
	kept := slices.DeleteFunc(markers, func(marker string) bool { return strings.HasPrefix(marker, "listType=") })
	return append(kept, listMarkers...)
}

func comment(comments protogen.Comments) string {
	return strings.TrimSpace(string(comments))
}

func upperFirst(name string) string {
	return strings.ToUpper(name[:1]) + name[1:]
}
