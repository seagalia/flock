package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
)

const listPageSize = 100

const (
	actionCreate = "create"
	actionUpdate = "update"
)

// change is one write. From is nil on a create.
type change struct {
	Kind, Name, Action string
	From, To           proto.Message
}

func (c change) String() string {
	return c.Action + " " + c.Kind + " " + c.Name
}

// listAll asks for pages until an empty one.
func listAll[T any](list func(offset int32) ([]T, error)) ([]T, error) {
	all := []T{}
	for offset := int32(0); ; offset += listPageSize {
		page, err := list(offset)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return all, nil
		}
		all = append(all, page...)
	}
}

// sameResource compares with defaults filled, as the controller stores them.
func sameResource(current, desired proto.Message) bool {
	current, desired = proto.Clone(current), proto.Clone(desired)
	helpers.ProvDefault(current)
	helpers.ProvDefault(desired)
	return proto.Equal(current, desired)
}

// applyFailure is the error of a run of writes. It holds the change that failed, the ones applied before it and the
// ones after it, which were never sent.
type applyFailure struct {
	Failed     change
	Applied    []string
	NotApplied []string
	Err        error
}

func (f *applyFailure) Error() string {
	summary := appliedSummary(f.Applied)
	if len(f.NotApplied) > 0 {
		summary += "\nnot applied:\n  - " + strings.Join(f.NotApplied, "\n  - ")
	}
	// an interrupted run never sent Failed, saying it failed would read as a write cut halfway
	if errors.Is(f.Err, errInterrupted) {
		return fmt.Sprintf("interrupted before %s\n%s", f.Failed, summary)
	}
	return fmt.Sprintf("%s: %s\n%s", f.Failed, f.Err, summary)
}

func (f *applyFailure) Unwrap() error { return f.Err }

// printApplied is the onApplied of the commands.
func printApplied(c change) {
	// create and update both take a "d" in the past tense.
	msg.Successf("%sd %s %s", c.Action, c.Kind, msg.Cyan(c.Name))
}

func appliedSummary(applied []string) string {
	if len(applied) == 0 {
		return "nothing was applied"
	}
	return "already applied:\n  - " + strings.Join(applied, "\n  - ")
}

// printChanges shows what the changes would do on stderr. A create prints the whole resource and an update prints
// the diff.
func printChanges(changes []change) error {
	for _, c := range changes {
		msg.Infof("%s", c)
		if c.Action == actionCreate {
			asYaml, err := ProtoToYaml(c.To)
			if err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, string(asYaml))
			continue
		}
		if err := PrintProtoDiff(os.Stderr, c.From, c.To); err != nil {
			return err
		}
	}
	return nil
}
