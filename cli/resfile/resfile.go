// Package resfile reads the multi-document yaml files of `flock apply` and the dashboard. Each document is a resource
// as the API defines it (protojson field names) plus a top-level `kind`.
package resfile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"buf.build/go/protovalidate"
	"go.yaml.in/yaml/v2"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	kyaml "sigs.k8s.io/yaml"

	backendv1 "github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	vipv1 "github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	vsv1 "github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

const (
	KindVs      = "vs"
	KindBackend = "backend"
	KindVip     = "vip"
)

// RawMessage keeps the decoding func yaml.v2 gives to UnmarshalYAML, so a document can be decoded a second time once
// its kind says which message it is.
type RawMessage struct {
	unmarshal func(v any) error
}

// Unmarshal fills v with the document, defaults included. `kind` is removed first because protojson refuses unknown
// fields.
func (r RawMessage) Unmarshal(v proto.Message) error {
	var fields map[string]any
	if err := r.unmarshal(&fields); err != nil {
		return err
	}
	delete(fields, "kind")

	// yaml.v2 decodes nested maps as map[any]any, which encoding/json cannot marshal. Marshaling back with yaml.v2 and
	// converting with sigs.k8s.io/yaml (as fileToMessage does) avoids walking the tree to convert keys by hand.
	asYAML, err := yaml.Marshal(fields)
	if err != nil {
		return err
	}
	asJSON, err := kyaml.YAMLToJSON(asYAML)
	if err != nil {
		return err
	}
	if err := protojson.Unmarshal(asJSON, v); err != nil {
		return err
	}
	helpers.ProvDefault(v)
	return nil
}

// KindModel is one document before its kind is known.
type KindModel struct {
	Kind string     `yaml:"kind"`
	Data RawMessage `yaml:"-"`
}

// UnmarshalYAML sits here and not on an inline RawMessage because yaml.v2 never calls it on inline fields.
func (k *KindModel) UnmarshalYAML(unmarshal func(any) error) error {
	var header struct {
		Kind string `yaml:"kind"`
	}
	if err := unmarshal(&header); err != nil {
		return err
	}
	k.Kind = header.Kind
	k.Data = RawMessage{unmarshal: unmarshal}
	return nil
}

// Document is one decoded resource of a file. Index starts at 1 and counts the empty documents too.
type Document struct {
	File    string
	Index   int
	Kind    string
	Message proto.Message
}

// Name is the name field of the resource.
func (d Document) Name() string {
	named, ok := d.Message.(interface{ GetName() string })
	if !ok {
		return ""
	}
	return named.GetName()
}

// UUID is the uuid field of the resource, empty when the document does not target an existing one.
func (d Document) UUID() string {
	identified, ok := d.Message.(interface{ GetUUID() string })
	if !ok {
		return ""
	}
	return identified.GetUUID()
}

// Problem is something wrong in a file. Index is 0 when the whole file is concerned.
type Problem struct {
	File    string
	Index   int
	Field   string
	Message string
}

func (p Problem) String() string {
	location := p.File
	if p.Index > 0 {
		location = fmt.Sprintf("%s document %d", p.File, p.Index)
	}
	if p.Field == "" {
		return fmt.Sprintf("%s: %s", location, p.Message)
	}
	return fmt.Sprintf("%s: %s: %s", location, p.Field, p.Message)
}

const expectedKinds = "expected vs, backend or vip"

func newMessage(kind string) proto.Message {
	switch kind {
	case KindVs:
		return &vsv1.VirtualServer{}
	case KindBackend:
		return &backendv1.Backend{}
	case KindVip:
		return &vipv1.VIP{}
	}
	return nil
}

// Parse splits a multi-document yaml file and decodes every document by its kind. Empty documents are skipped but
// keep their number.
func Parse(file string, content []byte) ([]Document, []Problem) {
	var docs []Document
	var problems []Problem

	decoder := yaml.NewDecoder(bytes.NewReader(content))
	for index := 1; ; index++ {
		var model KindModel
		err := decoder.Decode(&model)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// A syntax error leaves the decoder in an unknown place, so the rest of the file is not read.
			problems = append(problems, Problem{File: file, Index: index, Message: err.Error()})
			break
		}
		isEmpty := model.Data.unmarshal == nil
		if isEmpty {
			continue
		}

		if model.Kind == "" {
			problems = append(problems, Problem{File: file, Index: index, Field: "kind",
				Message: "missing kind, " + expectedKinds})
			continue
		}
		message := newMessage(model.Kind)
		if message == nil {
			problems = append(problems, Problem{File: file, Index: index, Field: "kind",
				Message: fmt.Sprintf("unknown kind %q, %s", model.Kind, expectedKinds)})
			continue
		}
		if err := model.Data.Unmarshal(message); err != nil {
			problems = append(problems, Problem{File: file, Index: index, Message: err.Error()})
			continue
		}
		docs = append(docs, Document{File: file, Index: index, Kind: model.Kind, Message: message})
	}
	return docs, problems
}

// Read parses files and folders. A folder adds its .yml and .yaml files in name order, without recursion.
func Read(paths ...string) ([]Document, []Problem) {
	var docs []Document
	var problems []Problem

	for _, path := range paths {
		files, err := filesOf(path)
		if err != nil {
			problems = append(problems, Problem{File: path, Message: err.Error()})
			continue
		}
		for _, file := range files {
			content, err := os.ReadFile(file)
			if err != nil {
				problems = append(problems, Problem{File: file, Message: err.Error()})
				continue
			}
			fileDocs, fileProblems := Parse(file, content)
			docs = append(docs, fileDocs...)
			problems = append(problems, fileProblems...)
		}
	}
	return docs, problems
}

func filesOf(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{path}, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		isYAML := extension == ".yml" || extension == ".yaml"
		if entry.IsDir() || !isYAML {
			continue
		}
		files = append(files, filepath.Join(path, entry.Name()))
	}
	slices.Sort(files)
	return files, nil
}

// Check validates every document against the protovalidate rules of the API and refuses two documents of the same
// kind and name. It reports every broken rule, not only the first.
func Check(docs []Document) []Problem {
	type identity struct{ kind, name string }
	var problems []Problem
	seen := map[identity]Document{}

	for _, doc := range docs {
		problems = append(problems, validationProblems(doc)...)
		problems = append(problems, lockProblems(doc)...)

		name := doc.Name()
		if name == "" {
			continue
		}
		key := identity{kind: doc.Kind, name: name}
		first, isDuplicate := seen[key]
		if !isDuplicate {
			seen[key] = doc
			continue
		}
		problems = append(problems, Problem{File: doc.File, Index: doc.Index, Field: "name",
			Message: fmt.Sprintf("%s %q is already defined by %s document %d", doc.Kind, name, first.File, first.Index)})
	}
	return problems
}

// lockProblems refuses the metadata key that locks a resource, because only the automatic provisioning sets it. The
// key is spelled here because this package cannot import the cli one.
func lockProblems(doc Document) []Problem {
	if _, isSet := helpers.GetMetadata(doc.Message)["flock.autoprovision"]; !isSet {
		return nil
	}
	return []Problem{{File: doc.File, Index: doc.Index, Field: "metadata",
		Message: "the key flock.autoprovision is reserved to automatic provisioning, remove it"}}
}

func validationProblems(doc Document) []Problem {
	err := helpers.Validate(doc.Message)
	if err == nil {
		return nil
	}
	var invalid *protovalidate.ValidationError
	if !errors.As(err, &invalid) {
		return []Problem{{File: doc.File, Index: doc.Index, Message: err.Error()}}
	}

	problems := make([]Problem, 0, len(invalid.Violations))
	for _, violation := range invalid.Violations {
		problems = append(problems, Problem{
			File:    doc.File,
			Index:   doc.Index,
			Field:   protovalidate.FieldPathString(violation.Proto.GetField()),
			Message: violation.Proto.GetMessage(),
		})
	}
	return problems
}
