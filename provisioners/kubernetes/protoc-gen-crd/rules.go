package main

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/durationpb"
)

const (
	// the conventions bound int64 to what a JSON number keeps exactly
	maxSafeInteger = int64(1<<53 - 1)

	// the HTTP header patterns of protovalidate, strict (the default) and loose
	headerNamePattern  = "^:?[0-9a-zA-Z!#$%&'*+.\\-^_|~\\x60]+$"
	headerValuePattern = "^[^\\x00-\\x08\\x0A-\\x1F\\x7F]*$"
	looseHeaderPattern = "^[^\\x00\\x0A\\x0D]+$"
)

// constraints is what the protovalidate rules of one field become in the CRD.
type constraints struct {
	markers  []string
	celRules []celRule
	// bounded tells the field has a maximum, which the CRD needs on every string, list and map: the conventions ask
	// for it, and the apiserver refuses CEL rules whose cost it can't bound
	bounded bool
	// zeroValid tells the zero value passes the rules, so an absent field can stay optional
	zeroValid bool
}

// typeBounds are the limits the CRD type itself needs: no negative value for an unsigned proto type, and no int64
// beyond what JSON keeps exactly.
type typeBounds struct {
	min *int64
	max *int64
}

type bound struct {
	value     int64
	exclusive bool
}

// numberRange is the gt, gte, lt, lte, in and not_in rules of a number or a duration, in the unit of the CRD field.
type numberRange struct {
	lower *bound
	upper *bound
	in    []int64
	notIn []int64
}

func fieldRules(field *protogen.Field) *validate.FieldRules {
	return fieldRulesOf(field.Desc)
}

func fieldRulesOf(field protoreflect.FieldDescriptor) *validate.FieldRules {
	rules, _ := proto.GetExtension(field.Options(), validate.E_Field).(*validate.FieldRules)
	if rules.GetIgnore() == validate.Ignore_IGNORE_ALWAYS {
		return nil
	}
	return rules
}

func oneofRules(oneof *protogen.Oneof) *validate.OneofRules {
	rules, _ := proto.GetExtension(oneof.Desc.Options(), validate.E_Oneof).(*validate.OneofRules)
	return rules
}

// unsupportedRules names the rules set in rules beyond supported. Each one stops the generation, a rule the plugin
// doesn't know would otherwise vanish from the CRD without a word.
func unsupportedRules(rules proto.Message, supported ...protoreflect.Name) []protoreflect.Name {
	var names []protoreflect.Name
	if rules == nil || !rules.ProtoReflect().IsValid() {
		return nil
	}
	rules.ProtoReflect().Range(func(field protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		isDocumentation := field.Name() == "example"
		if !isDocumentation && !slices.Contains(supported, field.Name()) {
			names = append(names, field.Name())
		}
		return true
	})
	return names
}

func (b *builder) failUnsupported(field *protogen.Field, rules proto.Message, supported ...protoreflect.Name) {
	for _, name := range unsupportedRules(rules, supported...) {
		b.fail(field.Desc.FullName(), "rule %s is not supported", name)
	}
}

func (b *builder) checkFieldRules(field *protogen.Field) {
	rules := fieldRules(field)
	supported := []protoreflect.Name{"required", "ignore", "cel"}
	if rules != nil && rules.HasType() {
		supported = append(supported, rules.ProtoReflect().WhichOneof(rules.ProtoReflect().Descriptor().Oneofs().ByName("type")).Name())
	}
	b.failUnsupported(field, rules, supported...)
}

func (b *builder) stringConstraints(field *protogen.Field, rules *validate.StringRules) constraints {
	c := b.stringRules(field, rules)
	if fieldRules(field).GetIgnore() == validate.Ignore_IGNORE_IF_ZERO_VALUE {
		c.zeroValid = true
	}
	return c
}

// stringRules are the rules of one string, a field or an item of a list.
func (b *builder) stringRules(field *protogen.Field, rules *validate.StringRules) constraints {
	b.failUnsupported(field, rules, "min_len", "max_len", "max_bytes", "pattern", "prefix", "in", "not_in", "ip",
		"well_known_regex", "strict")
	c := constraints{zeroValid: true}
	if rules.HasMinLen() {
		c.markers = append(c.markers, fmt.Sprintf("kubebuilder:validation:MinLength=%d", rules.GetMinLen()))
		c.zeroValid = rules.GetMinLen() == 0
	}
	if rules.HasMaxLen() {
		c.markers = append(c.markers, fmt.Sprintf("kubebuilder:validation:MaxLength=%d", rules.GetMaxLen()))
		c.bounded = true
	}
	// a character takes at least one byte, so the byte limit is also a length limit. The exact byte count is left to
	// the controller: a bytes() rule costs the length of the string, which spends the CEL budget of the CRD
	if rules.HasMaxBytes() {
		if !rules.HasMaxLen() {
			c.markers = append(c.markers, fmt.Sprintf("kubebuilder:validation:MaxLength=%d", rules.GetMaxBytes()))
		}
		c.bounded = true
	}
	if rules.HasPattern() {
		c.markers = append(c.markers, "kubebuilder:validation:Pattern="+strconv.Quote(rules.GetPattern()))
		c.zeroValid = c.zeroValid && matchesEmpty(rules.GetPattern())
	}
	if rules.HasPrefix() {
		c.celRules = append(c.celRules, celRule{
			rule:    fmt.Sprintf("self.startsWith(%s)", celString(rules.GetPrefix())),
			message: fmt.Sprintf("must start with %q", rules.GetPrefix()),
		})
		c.zeroValid = c.zeroValid && rules.GetPrefix() == ""
	}
	if len(rules.GetIn()) > 0 {
		c.markers = append(c.markers, "kubebuilder:validation:Enum="+strings.Join(rules.GetIn(), ";"))
		c.zeroValid = c.zeroValid && slices.Contains(rules.GetIn(), "")
		c.bounded = true
	}
	if len(rules.GetNotIn()) > 0 {
		c.celRules = append(c.celRules, celRule{
			rule:    fmt.Sprintf("!(self in [%s])", celStrings(rules.GetNotIn())),
			message: "must not be one of " + strings.Join(rules.GetNotIn(), ", "),
		})
		c.zeroValid = c.zeroValid && !slices.Contains(rules.GetNotIn(), "")
	}
	b.wellKnownString(field, rules, &c)
	return c
}

func (b *builder) wellKnownString(field *protogen.Field, rules *validate.StringRules, c *constraints) {
	switch {
	case rules.GetIp():
		c.celRules = append(c.celRules, celRule{rule: "isIP(self)", message: "must be an IP address"})
		c.zeroValid = false
	case rules.GetWellKnownRegex() == validate.KnownRegex_KNOWN_REGEX_HTTP_HEADER_NAME:
		pattern := headerNamePattern
		if rules.HasStrict() && !rules.GetStrict() {
			pattern = looseHeaderPattern
		}
		c.markers = append(c.markers, "kubebuilder:validation:Pattern="+strconv.Quote(pattern))
		c.zeroValid = false
	case rules.GetWellKnownRegex() == validate.KnownRegex_KNOWN_REGEX_HTTP_HEADER_VALUE:
		pattern := headerValuePattern
		if rules.HasStrict() && !rules.GetStrict() {
			pattern = looseHeaderPattern
		}
		c.markers = append(c.markers, "kubebuilder:validation:Pattern="+strconv.Quote(pattern))
		c.zeroValid = c.zeroValid && matchesEmpty(pattern)
	case rules.HasWellKnown():
		b.fail(field.Desc.FullName(), "well known string rule %s is not supported",
			rules.ProtoReflect().WhichOneof(rules.ProtoReflect().Descriptor().Oneofs().ByName("well_known")).Name())
	}
}

func matchesEmpty(pattern string) bool {
	matches, err := regexp.MatchString(pattern, "")
	return err == nil && matches
}

func (b *builder) bytesConstraints(field *protogen.Field, rules *validate.BytesRules) constraints {
	b.failUnsupported(field, rules, "min_len", "max_len")
	c := constraints{zeroValid: rules.GetMinLen() == 0}
	// the CRD holds bytes in base64, four characters for every three bytes
	if rules.HasMinLen() {
		c.markers = append(c.markers, fmt.Sprintf("kubebuilder:validation:MinLength=%d", base64Length(rules.GetMinLen())))
	}
	if rules.HasMaxLen() {
		c.markers = append(c.markers, fmt.Sprintf("kubebuilder:validation:MaxLength=%d", base64Length(rules.GetMaxLen())))
		c.bounded = true
	}
	return c
}

func base64Length(byteCount uint64) uint64 {
	return (byteCount + 2) / 3 * 4
}

func (b *builder) enumConstraints(field *protogen.Field, rules *validate.EnumRules) constraints {
	b.failUnsupported(field, rules, "defined_only", "not_in")
	// the Enum marker of the type already refuses an undefined value, and an empty value is the absent field
	c := constraints{bounded: true, zeroValid: true}
	var excluded []string
	for _, number := range rules.GetNotIn() {
		value := field.Enum.Desc.Values().ByNumber(protoreflect.EnumNumber(number))
		if value == nil {
			b.fail(field.Desc.FullName(), "not_in names %d, which %s doesn't define", number, field.Enum.Desc.FullName())
			continue
		}
		excluded = append(excluded, enumValue(string(value.Name())))
	}
	if len(excluded) > 0 {
		c.celRules = append(c.celRules, celRule{
			rule:    fmt.Sprintf("!(self in [%s])", celStrings(excluded)),
			message: "must not be one of " + strings.Join(excluded, ", "),
		})
	}
	return c
}

func (b *builder) numberConstraints(field *protogen.Field, rules proto.Message, limits typeBounds) constraints {
	b.failUnsupported(field, rules, "gt", "gte", "lt", "lte", "in", "not_in")
	numbers := b.readNumberRange(field, rules, func(value protoreflect.Value) (int64, error) {
		if unsigned, isUnsigned := value.Interface().(uint64); isUnsigned {
			return int64(unsigned), nil
		}
		if unsigned, isUnsigned := value.Interface().(uint32); isUnsigned {
			return int64(unsigned), nil
		}
		return value.Int(), nil
	})
	return numbers.constraints(limits)
}

func (b *builder) durationConstraints(field *protogen.Field, rules *validate.DurationRules) constraints {
	b.failUnsupported(field, rules, "gt", "gte", "lt", "lte")
	numbers := b.readNumberRange(field, rules, func(value protoreflect.Value) (int64, error) {
		duration, _ := value.Message().Interface().(*durationpb.Duration)
		nanos := duration.AsDuration().Nanoseconds()
		if nanos%1_000_000 != 0 {
			return 0, fmt.Errorf("%s is not a whole number of milliseconds", duration.AsDuration())
		}
		return nanos / 1_000_000, nil
	})
	// int32 milliseconds stop at about 24 days
	c := numbers.constraints(typeBounds{})
	if numbers.upper != nil && numbers.upper.value > math.MaxInt32 {
		b.fail(field.Desc.FullName(), "bound beyond the int32 milliseconds of the CRD")
	}
	return c
}

func (b *builder) readNumberRange(field *protogen.Field, rules proto.Message,
	toInt func(protoreflect.Value) (int64, error)) numberRange {
	var numbers numberRange
	if rules == nil || !rules.ProtoReflect().IsValid() {
		return numbers
	}
	message := rules.ProtoReflect()
	read := func(name protoreflect.Name) (int64, bool) {
		descriptor := message.Descriptor().Fields().ByName(name)
		if descriptor == nil || !message.Has(descriptor) {
			return 0, false
		}
		value, err := toInt(message.Get(descriptor))
		if err != nil {
			b.fail(field.Desc.FullName(), "rule %s: %v", name, err)
			return 0, false
		}
		return value, true
	}
	if value, isSet := read("gt"); isSet {
		numbers.lower = &bound{value: value, exclusive: true}
	}
	if value, isSet := read("gte"); isSet {
		numbers.lower = &bound{value: value}
	}
	if value, isSet := read("lt"); isSet {
		numbers.upper = &bound{value: value, exclusive: true}
	}
	if value, isSet := read("lte"); isSet {
		numbers.upper = &bound{value: value}
	}
	numbers.in = readList(message, "in", toInt)
	numbers.notIn = readList(message, "not_in", toInt)
	return numbers
}

func readList(message protoreflect.Message, name protoreflect.Name, toInt func(protoreflect.Value) (int64, error)) []int64 {
	descriptor := message.Descriptor().Fields().ByName(name)
	if descriptor == nil {
		return nil
	}
	list := message.Get(descriptor).List()
	values := make([]int64, 0, list.Len())
	for i := range list.Len() {
		value, err := toInt(list.Get(i))
		if err == nil {
			values = append(values, value)
		}
	}
	return values
}

// isExclusiveRange tells a lower bound above the upper one, which protovalidate reads as "outside the range".
func (r numberRange) isExclusiveRange() bool {
	if r.lower == nil || r.upper == nil {
		return false
	}
	touching := r.lower.value == r.upper.value && (r.lower.exclusive || r.upper.exclusive)
	return r.lower.value > r.upper.value || touching
}

func (r numberRange) accepts(value int64) bool {
	aboveLower := r.lower == nil || value > r.lower.value || (value == r.lower.value && !r.lower.exclusive)
	belowUpper := r.upper == nil || value < r.upper.value || (value == r.upper.value && !r.upper.exclusive)
	inRange := aboveLower && belowUpper
	if r.isExclusiveRange() {
		inRange = aboveLower || belowUpper
	}
	isListed := len(r.in) == 0 || slices.Contains(r.in, value)
	return inRange && isListed && !slices.Contains(r.notIn, value)
}

func (r numberRange) constraints(limits typeBounds) constraints {
	c := constraints{bounded: true, zeroValid: r.accepts(0)}
	if r.isExclusiveRange() {
		c.celRules = append(c.celRules, celRule{
			rule:    fmt.Sprintf("self %s %d || self %s %d", upperOperator(r.upper), r.upper.value, lowerOperator(r.lower), r.lower.value),
			message: fmt.Sprintf("must be %s %d or %s %d", upperOperator(r.upper), r.upper.value, lowerOperator(r.lower), r.lower.value),
		})
		c.markers = append(c.markers, limits.markers()...)
	} else {
		c.markers = append(c.markers, rangeMarkers(r.lower, limits.min, "Minimum", "ExclusiveMinimum")...)
		c.markers = append(c.markers, rangeMarkers(r.upper, limits.max, "Maximum", "ExclusiveMaximum")...)
	}
	if len(r.in) > 0 {
		c.markers = append(c.markers, "kubebuilder:validation:Enum="+joinInts(r.in, ";"))
	}
	if len(r.notIn) > 0 {
		c.celRules = append(c.celRules, celRule{
			rule:    fmt.Sprintf("!(self in [%s])", joinInts(r.notIn, ", ")),
			message: "must not be one of " + joinInts(r.notIn, ", "),
		})
	}
	return c
}

func (limits typeBounds) markers() []string {
	var markers []string
	if limits.min != nil {
		markers = append(markers, fmt.Sprintf("kubebuilder:validation:Minimum=%d", *limits.min))
	}
	if limits.max != nil {
		markers = append(markers, fmt.Sprintf("kubebuilder:validation:Maximum=%d", *limits.max))
	}
	return markers
}

func rangeMarkers(limit *bound, typeLimit *int64, name, exclusiveName string) []string {
	if limit == nil && typeLimit == nil {
		return nil
	}
	if limit == nil {
		return []string{fmt.Sprintf("kubebuilder:validation:%s=%d", name, *typeLimit)}
	}
	markers := []string{fmt.Sprintf("kubebuilder:validation:%s=%d", name, limit.value)}
	if limit.exclusive {
		markers = append(markers, fmt.Sprintf("kubebuilder:validation:%s=true", exclusiveName))
	}
	return markers
}

func lowerOperator(limit *bound) string {
	if limit.exclusive {
		return ">"
	}
	return ">="
}

func upperOperator(limit *bound) string {
	if limit.exclusive {
		return "<"
	}
	return "<="
}

func (b *builder) repeatedConstraints(field *protogen.Field, rules *validate.RepeatedRules) constraints {
	b.failUnsupported(field, rules, "min_items", "max_items", "unique", "items")
	c := constraints{zeroValid: rules.GetMinItems() == 0, markers: []string{"listType=atomic"}}
	if rules.HasMinItems() {
		c.markers = append(c.markers, fmt.Sprintf("kubebuilder:validation:MinItems=%d", rules.GetMinItems()))
	}
	if rules.HasMaxItems() {
		c.markers = append(c.markers, fmt.Sprintf("kubebuilder:validation:MaxItems=%d", rules.GetMaxItems()))
		c.bounded = true
	}
	if rules.GetUnique() {
		c.markers = replaceListType(c.markers, "listType=set")
	}
	if field.Desc.Kind() == protoreflect.StringKind && !isReference(field) {
		itemsBounded := b.stringItems(field, rules.GetItems(), &c)
		c.bounded = c.bounded && itemsBounded
	}
	return c
}

// stringItems turns the rules on the items of a list of strings into items markers, where self is the item, and tells
// whether they bound each item.
func (b *builder) stringItems(field *protogen.Field, items *validate.FieldRules, c *constraints) bool {
	b.failUnsupported(field, items, "string", "cel")
	item := b.stringRules(field, items.GetString())
	for _, marker := range item.markers {
		c.markers = append(c.markers, strings.Replace(marker, "kubebuilder:validation:", "kubebuilder:validation:items:", 1))
	}
	itemRules := item.celRules
	for _, rule := range items.GetCel() {
		expression, found := celExpressions[rule.GetId()]
		if !found {
			b.fail(field.Desc.FullName(), "CEL rule %q missing from celExpressions", rule.GetId())
			continue
		}
		itemRules = append(itemRules, celRule{rule: expression, message: rule.GetMessage()})
	}
	for _, rule := range itemRules {
		c.markers = append(c.markers, fmt.Sprintf("kubebuilder:validation:items:XValidation:rule=%s,message=%s",
			strconv.Quote(rule.rule), strconv.Quote(rule.message)))
	}
	return item.bounded
}

// mapConstraints bounds a map<string, string>. The keys and values have no schema of their own in a CRD, CEL rules
// check them, which costs little since size() is constant.
func (b *builder) mapConstraints(field *protogen.Field, rules *validate.MapRules) constraints {
	b.failUnsupported(field, rules, "min_pairs", "max_pairs", "keys", "values")
	c := constraints{zeroValid: rules.GetMinPairs() == 0}
	if rules.HasMinPairs() {
		c.markers = append(c.markers, fmt.Sprintf("kubebuilder:validation:MinProperties=%d", rules.GetMinPairs()))
	}
	if rules.HasMaxPairs() {
		c.markers = append(c.markers, fmt.Sprintf("kubebuilder:validation:MaxProperties=%d", rules.GetMaxPairs()))
	}
	keysBounded := b.mapStrings(field, rules.GetKeys(), "k", "keys", &c)
	valuesBounded := b.mapStrings(field, rules.GetValues(), "self[k]", "values", &c)
	c.bounded = rules.HasMaxPairs() && keysBounded && valuesBounded
	return c
}

func (b *builder) mapStrings(field *protogen.Field, rules *validate.FieldRules, item, what string, c *constraints) bool {
	b.failUnsupported(field, rules, "string")
	valueRules := rules.GetString()
	b.failUnsupported(field, valueRules, "min_len", "max_len")
	if valueRules.HasMinLen() {
		c.celRules = append(c.celRules, celRule{
			rule:    fmt.Sprintf("self.all(k, %s.size() >= %d)", item, valueRules.GetMinLen()),
			message: fmt.Sprintf("%s must be at least %d characters", what, valueRules.GetMinLen()),
		})
	}
	if !valueRules.HasMaxLen() {
		return false
	}
	c.celRules = append(c.celRules, celRule{
		rule:    fmt.Sprintf("self.all(k, %s.size() <= %d)", item, valueRules.GetMaxLen()),
		message: fmt.Sprintf("%s must be at most %d characters", what, valueRules.GetMaxLen()),
	})
	return true
}

// celString quotes with double quotes, since gofmt turns two single quotes into a typographic quote in the doc
// comments holding the markers.
func celString(value string) string {
	return strconv.Quote(value)
}

func celStrings(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, celString(value))
	}
	return strings.Join(quoted, ", ")
}

func joinInts(values []int64, separator string) string {
	texts := make([]string, 0, len(values))
	for _, value := range values {
		texts = append(texts, strconv.FormatInt(value, 10))
	}
	return strings.Join(texts, separator)
}
