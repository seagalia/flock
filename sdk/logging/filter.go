package logging

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/rs/zerolog"
)

// everyLevel marks a "!component" rule without level: every level of the component is shown
const everyLevel zerolog.Level = 100

// componentFilter applies filter_components rules to the JSON lines it forwards to next:
//   - "comp" hides trace lines of comp
//   - "comp=lvl" hides lines of comp at lvl and more verbose
//   - "!comp" switches to allow-list mode: only "!" components are shown
//   - "!comp=lvl" in allow-list mode, only shows lvl for comp
//
// Lines without component are always shown.
type componentFilter struct {
	next     io.Writer
	hidden   map[string]zerolog.Level
	selected map[string]zerolog.Level
	// decisions per component and level, rules never change once built
	decisions sync.Map
}

type decisionKey struct {
	component string
	level     zerolog.Level
}

func newComponentFilter(rules []string, next io.Writer) (*componentFilter, error) {
	f := &componentFilter{
		next:     next,
		hidden:   make(map[string]zerolog.Level),
		selected: make(map[string]zerolog.Level),
	}
	for _, rule := range rules {
		component, isSelected := strings.CutPrefix(rule, "!")
		rules := f.hidden
		if isSelected {
			rules = f.selected
		}
		name, levelName, hasLevel := strings.Cut(component, "=")
		if !hasLevel && isSelected {
			rules[name] = everyLevel
			continue
		}
		if !hasLevel {
			rules[name] = zerolog.TraceLevel
			continue
		}
		level, err := ParseLevel(levelName)
		if err != nil {
			return nil, fmt.Errorf("parsing log level for component %s: %w", name, err)
		}
		rules[name] = level
	}
	return f, nil
}

func (f *componentFilter) Write(line []byte) (int, error) {
	var header struct {
		Level     string `json:"level"`
		Component string `json:"component"`
	}
	// a line we cannot read is shown rather than lost
	if json.Unmarshal(line, &header) != nil || header.Component == "" {
		return f.next.Write(line)
	}
	level, err := zerolog.ParseLevel(header.Level)
	if err != nil {
		return f.next.Write(line)
	}
	if !f.isShown(header.Component, level) {
		return len(line), nil
	}
	return f.next.Write(line)
}

func (f *componentFilter) isShown(component string, level zerolog.Level) bool {
	key := decisionKey{component: component, level: level}
	if shown, ok := f.decisions.Load(key); ok {
		return shown.(bool)
	}
	shown := f.decide(component, level)
	f.decisions.Store(key, shown)
	return shown
}

// in zerolog a lower level is more verbose, hiding "from lvl" means level <= lvl
func (f *componentFilter) decide(component string, level zerolog.Level) bool {
	hiddenFrom, isHidden := f.hidden[component]
	isHiddenLevel := isHidden && level <= hiddenFrom

	selectedLevel, isSelected := f.selected[component]
	isSelectedLevel := isSelected && (selectedLevel == everyLevel || selectedLevel == level)
	if isSelectedLevel {
		return !isHiddenLevel
	}
	isAllowListMode := len(f.selected) > 0
	if isAllowListMode {
		return false
	}
	return !isHiddenLevel
}
