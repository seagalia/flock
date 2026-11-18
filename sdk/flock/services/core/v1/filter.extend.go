package svccore

import (
	"strings"

	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
)

type ListFilters []*ListFilter

func (x *ListFilter) Match(msg proto.Message) bool {
	if x.IsMetadata {
		return x.MatchFromMap(helpers.GetMetadata(msg))
	}
	m := map[string]string{
		"name": helpers.GetName(msg),
		"uuid": helpers.GetIdentifier(msg),
	}
	return x.MatchFromMap(m)
}

func (x *ListFilter) MatchFromMap(m map[string]string) bool {
	for key, value := range m {
		if x.GetKey() != key {
			continue
		}
		if x.OperatorMatch(value, x.Value, x.Operator) {
			return true
		}
	}
	return false
}

func (x *ListFilter) OperatorMatch(value, toMatch string, op ListFilter_Operator) bool {
	switch op {
	case ListFilter_EQUAL:
		if value == toMatch {
			return true
		}
	case ListFilter_NOT_EQUAL:
		if value != toMatch {
			return true
		}
	case ListFilter_LIKE:
		if strings.Contains(value, toMatch) {
			return true
		}
	case ListFilter_NOT_LIKE:
		if !strings.Contains(value, toMatch) {
			return true
		}
	}
	return false
}

func (x *ListFilters) MatchOr(msg proto.Message) bool {
	if x == nil || len(*x) == 0 {
		return true
	}
	for _, filter := range *x {
		if filter.Match(msg) {
			return true
		}
	}
	return false
}

func (x *ListFilters) MatchAnd(msg proto.Message) bool {
	if x == nil || len(*x) == 0 {
		return true
	}
	for _, filter := range *x {
		if !filter.Match(msg) {
			return false
		}
	}
	return true
}
