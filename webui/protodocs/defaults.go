package main

import (
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/seagalia/flock/sdk/helpers"

	// the packages whose messages have a ProvDefault, linked so their types are in the global registry
	_ "github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	_ "github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	_ "github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	_ "github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
)

const durationFullName = "google.protobuf.Duration"

// collectDefaults runs ProvDefault, the defaults the controller gives every received message, on an empty message of
// each type and keeps the fields it set, by full field name, as the text a form input shows. Only fields holding one
// value are kept, and a default copied from another field (unhealthy_interval takes the interval) is left out since
// no fixed text tells it.
func collectDefaults(fieldDefaults map[string]string) {
	protoregistry.GlobalTypes.RangeMessages(func(messageType protoreflect.MessageType) bool {
		if !strings.HasPrefix(string(messageType.Descriptor().FullName()), "flock.") {
			return true
		}
		defaulted := withDefaults(messageType, nil)
		for field, value := range defaulted {
			shifted := map[protoreflect.FieldDescriptor]protoreflect.Value{}
			for other, otherValue := range defaulted {
				if other != field {
					shifted[other] = shiftedValue(other, otherValue)
				}
			}
			again, isSetAgain := withDefaults(messageType, shifted)[field]
			isCopied := !isSetAgain || !again.Equal(value)
			if isCopied {
				continue
			}
			fieldDefaults[string(field.FullName())] = defaultText(field, value)
		}
		return true
	})
}

// withDefaults gives the one value fields ProvDefault sets on a message holding preset
func withDefaults(messageType protoreflect.MessageType, preset map[protoreflect.FieldDescriptor]protoreflect.Value) map[protoreflect.FieldDescriptor]protoreflect.Value {
	message := messageType.New()
	for field, value := range preset {
		message.Set(field, value)
	}
	helpers.ProvDefault(message.Interface())
	defaulted := map[protoreflect.FieldDescriptor]protoreflect.Value{}
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		_, isPreset := preset[field]
		if !isPreset && isOneValue(field) {
			defaulted[field] = value
		}
		return true
	})
	return defaulted
}

// isOneValue tells the fields a form edits with one input, the only ones a placeholder can show a default for
func isOneValue(field protoreflect.FieldDescriptor) bool {
	if field.IsList() || field.IsMap() {
		return false
	}
	switch field.Kind() {
	case protoreflect.MessageKind:
		return field.Message().FullName() == durationFullName
	case protoreflect.BoolKind, protoreflect.EnumKind, protoreflect.BytesKind, protoreflect.GroupKind:
		return false
	default:
		return true
	}
}

// shiftedValue is another value of the same field, a default that follows it was copied from it
func shiftedValue(field protoreflect.FieldDescriptor, value protoreflect.Value) protoreflect.Value {
	switch field.Kind() {
	case protoreflect.MessageKind:
		duration := value.Message().Interface().(*durationpb.Duration).AsDuration()
		return protoreflect.ValueOfMessage(durationpb.New(duration + time.Second).ProtoReflect())
	case protoreflect.StringKind:
		return protoreflect.ValueOfString(value.String() + "-shifted")
	case protoreflect.FloatKind:
		return protoreflect.ValueOfFloat32(float32(value.Float()) + 1)
	case protoreflect.DoubleKind:
		return protoreflect.ValueOfFloat64(value.Float() + 1)
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return protoreflect.ValueOfInt32(int32(value.Int()) + 1)
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return protoreflect.ValueOfInt64(value.Int() + 1)
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return protoreflect.ValueOfUint32(uint32(value.Uint()) + 1)
	default:
		return protoreflect.ValueOfUint64(value.Uint() + 1)
	}
}

// defaultText writes a duration in its JSON form (30s), as the form input reads it
func defaultText(field protoreflect.FieldDescriptor, value protoreflect.Value) string {
	if field.Kind() != protoreflect.MessageKind {
		return fmt.Sprint(value.Interface())
	}
	text, err := protojson.Marshal(value.Message().Interface())
	if err != nil {
		return ""
	}
	return strings.Trim(string(text), `"`)
}
