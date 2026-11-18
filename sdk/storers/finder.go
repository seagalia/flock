package storers

import (
	"cmp"
	"context"
	"slices"

	"strings"

	"github.com/seagalia/flock/sdk/errors"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
)

func FindByName[T proto.Message](ctx context.Context, name string, storer Storer[T], listOpt *ListOptions) (T, error) {
	if storerFind, ok := storer.(StorerFindName[T]); ok {
		return storerFind.FindByName(ctx, name)
	}
	var message T
	messages, err := storer.List(ctx, listOpt)
	if err != nil {
		return message, err
	}
	for _, message := range messages {
		if helpers.GetName(message) == name {
			return message, nil
		}
	}
	return message, errors.NewErrNotFound(helpers.TypeUrl[T](), name)
}

// matchFilters matches a message on any of its name and uuid filters, like the sql store does, so one list can read
// several messages.
func matchFilters(message proto.Message, filters []*svccore.ListFilter) bool {
	hasFilter := false
	for _, filter := range filters {
		var field string
		switch {
		case filter.IsMetadata:
			continue
		case filter.Key == FilterNameKey:
			field = helpers.GetName(message)
		case filter.Key == FilterUuidKey:
			field = helpers.GetIdentifier(message)
		default:
			continue
		}
		hasFilter = true
		if matchFilter(field, filter) {
			return true
		}
	}
	return !hasFilter
}

func matchFilter(field string, filter *svccore.ListFilter) bool {
	switch filter.Operator {
	case svccore.ListFilter_EQUAL:
		return field == filter.Value
	case svccore.ListFilter_NOT_EQUAL:
		return field != filter.Value
	case svccore.ListFilter_LIKE:
		return strings.Contains(field, filter.Value)
	case svccore.ListFilter_NOT_LIKE:
		return !strings.Contains(field, filter.Value)
	}
	return false
}

// paginate applies the limit and offset of opt like the sql store does. Memory and file stores range over a
// sync.Map in random order: messages are sorted by identifier so that pages don't overlap.
func paginate[T proto.Message](messages []T, opt *ListOptions) []T {
	slices.SortFunc(messages, func(a, b T) int {
		return cmp.Compare(helpers.GetIdentifier(a), helpers.GetIdentifier(b))
	})
	if opt == nil {
		return messages
	}
	if opt.Offset >= len(messages) {
		return []T{}
	}
	if opt.Offset > 0 {
		messages = messages[opt.Offset:]
	}
	if opt.Limit > 0 && opt.Limit < len(messages) {
		messages = messages[:opt.Limit]
	}
	return messages
}
