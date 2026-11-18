package main

import (
	"slices"
	"strings"
)

const commonGroup = "common"

// fileGroup is what one pair of generated files holds: the types of one kind, or the types several kinds share.
type fileGroup struct {
	name  string
	enums []*goEnum
	types []*goType
}

// fileGroups files each type and enum under the kind reaching it (`backend` for BackendSpec), or under common when
// several kinds reach it, so a kind's files never depend on another kind's.
func (b *builder) fileGroups() []*fileGroup {
	typesByName := map[string]*goType{}
	for _, t := range b.types {
		typesByName[t.name] = t
	}
	groupOf := map[string]string{}
	var visit func(name, group string)
	visit = func(name, group string) {
		previous, seen := groupOf[name]
		isFiledThere := previous == group || previous == commonGroup
		if seen && isFiledThere {
			return
		}
		if seen {
			group = commonGroup
		}
		groupOf[name] = group
		t, isType := typesByName[name]
		if !isType {
			return
		}
		for _, field := range t.fields {
			visit(field.elem.GoName, group)
		}
	}
	for _, root := range b.rootTypes {
		visit(root.name, strings.ToLower(strings.TrimSuffix(root.name, "Spec")))
	}

	groups := map[string]*fileGroup{}
	groupNamed := func(name string) *fileGroup {
		if groups[name] == nil {
			groups[name] = &fileGroup{name: name}
		}
		return groups[name]
	}
	for _, enum := range b.enums {
		group := groupNamed(groupOf[enum.name])
		group.enums = append(group.enums, enum)
	}
	for _, t := range b.types {
		group := groupNamed(groupOf[t.name])
		group.types = append(group.types, t)
	}
	sorted := make([]*fileGroup, 0, len(groups))
	for _, group := range groups {
		sorted = append(sorted, group)
	}
	slices.SortFunc(sorted, func(a, b *fileGroup) int { return strings.Compare(a.name, b.name) })
	return sorted
}
