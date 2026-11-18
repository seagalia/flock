package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"charm.land/huh/v2"
	msg "github.com/ArthurHlt/messages"
	"github.com/gonvenience/ytbx"
	"github.com/homeport/dyff/pkg/dyff"
	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/tw"
	"github.com/seagalia/flock/cli/cplters"
	"github.com/seagalia/flock/cli/highlight"
	"github.com/seagalia/flock/cli/tui/tuicommon"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"go.yaml.in/yaml/v2"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	kyaml "sigs.k8s.io/yaml"
)

var emptyJsonRegex = regexp.MustCompile(`^\s*\{\s*\}\s*$`)
var enumType = map[string]string{
	"certificate": helpers.TypeUrl[*cert.Certificate](),
	"backend":     helpers.TypeUrl[*backend.Backend](),
	"instance":    helpers.TypeUrl[*instance.Instance](),
	"vip":         helpers.TypeUrl[*vip.VIP](),
	"vs":          helpers.TypeUrl[*vs.VirtualServer](),
	"zone":        helpers.TypeUrl[*zone.Zone](),
	"secret":      helpers.TypeUrl[*secret.Secret](),
	"endpoints":   helpers.TypeUrl[*backend.BackendEndpointStats](),
}

func FileToProto[T proto.Message](file string) (protoMsg T, loaded bool, err error) {
	var protoMsgDef T
	protoMsg = protoMsgDef.ProtoReflect().New().Interface().(T)
	loaded, err = fileToMessage(file, protoMsg)
	return protoMsg, loaded, err
}

// fileToMessage fills protoMsg from a json or yaml file and gives it its defaults. A missing or empty
// file is not an error: loaded tells if the file had content.
func fileToMessage(file string, protoMsg proto.Message) (loaded bool, err error) {
	content, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			helpers.ProvDefault(protoMsg)
			return false, nil
		}
		return false, fmt.Errorf("failed to read file %s: %w", file, err)
	}

	ext := strings.ToLower(filepath.Ext(file))
	if ext == ".yaml" || ext == ".yml" {
		content, err = kyaml.YAMLToJSON(content)
		if err != nil {
			return false, fmt.Errorf("failed to convert yaml to json file %s: %w", file, err)
		}
	}
	if strings.TrimSpace(string(content)) == "" || string(content) == "null" {
		return false, nil
	}

	if emptyJsonRegex.Match(content) {
		return false, nil
	}

	err = protojson.Unmarshal(content, protoMsg)
	if err != nil {
		return false, fmt.Errorf("failed to unmarshal json file %s: %w", file, err)
	}
	helpers.ProvDefault(protoMsg)
	return true, nil
}

func ProtoToYaml(pMsg proto.Message) ([]byte, error) {
	data, err := protojson.MarshalOptions{
		Multiline:       true,
		UseProtoNames:   true,
		Indent:          "  ",
		EmitUnpopulated: true,
	}.Marshal(pMsg)
	if err != nil {
		return nil, err
	}
	mapProto := make(map[string]any)
	err = json.Unmarshal(data, &mapProto)
	if err != nil {
		return nil, err
	}
	return yaml.Marshal(mapProto)
}

func NameFromAny(anyMsg *anypb.Any) string {
	data, err := protojson.MarshalOptions{
		Multiline:       true,
		UseProtoNames:   true,
		Indent:          "  ",
		EmitUnpopulated: true,
	}.Marshal(anyMsg)
	if err != nil {
		panic(err)
	}
	mapProto := make(map[string]any)
	err = json.Unmarshal(data, &mapProto)
	if err != nil {
		panic(err)
	}
	name, ok := mapProto["name"]
	if !ok {
		return ""
	}
	return fmt.Sprint(name)
}

func ProtoDiffReport(from, dest proto.Message) (dyff.Report, error) {
	var fromYaml, destYaml []byte
	var err error
	if from != nil {
		fromYaml, err = ProtoToYaml(from)
		if err != nil {
			return dyff.Report{}, err
		}
	} else {
		fromYaml = []byte("")
	}
	if dest != nil {
		destYaml, err = ProtoToYaml(dest)
		if err != nil {
			return dyff.Report{}, err
		}
	} else {
		destYaml = []byte("")
	}

	fromDoc, err := ytbx.LoadDocuments(fromYaml)
	if err != nil {
		return dyff.Report{}, fmt.Errorf("failed to load input files: %s", err.Error())
	}
	toDoc, err := ytbx.LoadDocuments(destYaml)
	if err != nil {
		return dyff.Report{}, fmt.Errorf("failed to load input files: %s", err.Error())
	}

	iFrom := ytbx.InputFile{
		Documents: fromDoc,
		Names:     []string{helpers.GetIdentifier(from)},
	}
	iTo := ytbx.InputFile{
		Documents: toDoc,
		Names:     []string{helpers.GetIdentifier(dest)},
	}

	return dyff.CompareInputFiles(iFrom, iTo,
		dyff.IgnoreOrderChanges(true),
	)
}

func PrintProtoDiff(w io.Writer, from, dest proto.Message) error {
	report, err := ProtoDiffReport(from, dest)
	if err != nil {
		return fmt.Errorf("failed to compare input files: %s", err.Error())
	}

	reportWriter := &dyff.HumanReport{
		Report:               report,
		NoTableStyle:         true,
		OmitHeader:           true,
		UseGoPatchPaths:      false,
		MinorChangeThreshold: 0.1,
	}
	return reportWriter.WriteReport(w)
}

func ProtoDiffContent(from, dest proto.Message) (string, error) {
	report, err := ProtoDiffReport(from, dest)
	if err != nil {
		return "", fmt.Errorf("failed to compare input files: %s", err.Error())
	}

	reportWriter := &dyff.HumanReport{
		Report:               report,
		NoTableStyle:         true,
		OmitHeader:           true,
		UseGoPatchPaths:      false,
		MinorChangeThreshold: 0.1,
	}
	buf := &bytes.Buffer{}

	err = reportWriter.WriteReport(buf)
	if err != nil {
		return "", fmt.Errorf("failed to write report: %s", err.Error())
	}
	return buf.String(), nil
}

func PrintCompleter(ctx context.Context, client *clients.Client, cplter cplters.Completer) error {
	err := cplter.Complete(ctx, client)
	if err != nil {
		return err
	}
	data, err := cplter.ToMap()
	if err != nil {
		return err
	}
	b, err := yaml.Marshal(data)
	if err != nil {
		return err
	}
	result, err := highlight.Highlight(bytes.NewBuffer(b))
	if err != nil {
		return fmt.Errorf("failed to highlight yaml: %w", err)
	}
	fmt.Println(result)
	return nil
}

func PrintProtoHuman(pMsg proto.Message) error {
	dataYml, err := ProtoToYaml(pMsg)
	if err != nil {
		return fmt.Errorf("failed to convert proto to yaml: %w", err)
	}
	result, err := highlight.Highlight(bytes.NewBuffer(dataYml))
	if err != nil {
		return fmt.Errorf("failed to highlight yaml: %w", err)
	}
	fmt.Println(result)
	return nil
}

func PrintProtoJson(pMsg proto.Message) error {
	data, err := protojson.MarshalOptions{
		Multiline:       true,
		Indent:          "  ",
		UseProtoNames:   true,
		EmitUnpopulated: true,
	}.Marshal(pMsg)
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func PrintProtoListJson[T proto.Message](msgs []T) error {
	data, err := helpers.MarshalListProtoMessage[T](protojson.MarshalOptions{
		Multiline:       true,
		Indent:          "  ",
		UseProtoNames:   true,
		EmitUnpopulated: true,
	}, msgs)
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

// MakeTableWriter gives a borderless table drawn in ascii, as the cli always printed them, styled further
// with table.Options(tableBordered(), tableRowLines(), ...)
func MakeTableWriter(headers []string, writers ...io.Writer) *tablewriter.Table {
	writer := msg.Output()
	if len(writers) > 0 {
		writer = writers[0]
	}
	table := tablewriter.NewTable(writer,
		tablewriter.WithRendition(tw.Rendition{
			Borders: tw.BorderNone,
			Symbols: tw.NewSymbols(tw.StyleASCII),
		}),
		tablewriter.WithRowAlignment(tw.AlignLeft),
	)
	table.Header(headers)
	return table
}

func tableBordered() tablewriter.Option {
	return tablewriter.WithRendition(tw.Rendition{Borders: tw.Border{Left: tw.On, Right: tw.On, Top: tw.On, Bottom: tw.On}})
}

func tableRowLines() tablewriter.Option {
	return tablewriter.WithRendition(tw.Rendition{Settings: tw.Settings{Separators: tw.Separators{BetweenRows: tw.On}}})
}

func tableWrap(wrap bool) tablewriter.Option {
	if wrap {
		return tablewriter.WithRowAutoWrap(tw.WrapNormal)
	}
	return tablewriter.WithRowAutoWrap(tw.WrapNone)
}

// tableMergedColumns merges the cells of columns that repeat the value of the row above
func tableMergedColumns(columns ...int) tablewriter.Option {
	merged := tw.NewMapper[int, bool]()
	for _, column := range columns {
		merged.Set(column, true)
	}
	return func(table *tablewriter.Table) {
		table.Configure(func(cfg *tablewriter.Config) {
			cfg.Row.Merging = tw.CellMerging{Mode: tw.MergeVertical, ByColumnIndex: merged}
		})
	}
}

func TypeFromShort(short string) string {
	return enumType[strings.ToLower(short)]
}

func ProtoSkeletonToFile(message proto.Message, filename string) error {
	b, err := protojson.MarshalOptions{
		Multiline:       true,
		Indent:          "  ",
		AllowPartial:    false,
		UseProtoNames:   true,
		UseEnumNumbers:  false,
		EmitUnpopulated: true,
		Resolver:        nil,
	}.Marshal(message)
	if err != nil {
		return err
	}
	helpers.ProvDefault(message)
	ext := strings.ToLower(filepath.Ext(filename))
	if ext == ".yml" || ext == ".yaml" {
		b, err = kyaml.JSONToYAML(b)
		if err != nil {
			return fmt.Errorf("failed to convert json to yaml: %w", err)
		}
	}

	return os.WriteFile(filename, b, 0644)
}

func boolFmtTable(bl bool) string {
	if bl {
		return msg.Cyan("Yes").String()
	}
	return msg.Yellow("No").String()
}

// DiffAndConfirm writes the diff on stderr: stdout only holds the output of the command, the json of --json included
func DiffAndConfirm(from, dest proto.Message, force bool) (bool, error) {
	_, _ = fmt.Fprintf(os.Stderr, "%s Change to be made:\n━━━━━\n", msg.Cyan("Info"))
	err := PrintProtoDiff(os.Stderr, from, dest)
	if err != nil {
		return false, err
	}
	if force {
		return true, nil
	}
	confirm := false
	err = confirmChanges("Do you confirm theses changes?", &confirm)
	if err != nil {
		return false, err
	}
	return confirm, nil
}

func stringFiltersToFiltersMeta(filters []string) []*svccore.ListFilter {
	if len(filters) == 0 {
		return nil
	}
	filtersMeta := make([]*svccore.ListFilter, 0)
	for _, filter := range filters {
		filterMeta := stringFilterToFilterMeta(filter)
		if filterMeta != nil {
			filtersMeta = append(filtersMeta, filterMeta)
		}
	}
	return filtersMeta
}

func stringFilterToFilterMeta(filter string) *svccore.ListFilter {
	if filter == "" {
		return nil
	}
	splitted := strings.SplitN(filter, "==", 2)
	if len(splitted) == 2 {
		return &svccore.ListFilter{
			Key:        strings.TrimSpace(splitted[0]),
			Value:      strings.TrimSpace(splitted[1]),
			Operator:   svccore.ListFilter_EQUAL,
			IsMetadata: true,
		}
	}
	splitted = strings.SplitN(filter, "!=", 2)
	if len(splitted) == 2 {
		return &svccore.ListFilter{
			Key:        strings.TrimSpace(splitted[0]),
			Value:      strings.TrimSpace(splitted[1]),
			Operator:   svccore.ListFilter_NOT_EQUAL,
			IsMetadata: true,
		}
	}
	splitted = strings.SplitN(filter, "~=", 2)
	if len(splitted) == 2 {
		return &svccore.ListFilter{
			Key:        strings.TrimSpace(splitted[0]),
			Value:      strings.TrimSpace(splitted[1]),
			Operator:   svccore.ListFilter_LIKE,
			IsMetadata: true,
		}
	}
	splitted = strings.SplitN(filter, "!~", 2)
	if len(splitted) == 2 {
		return &svccore.ListFilter{
			Key:        strings.TrimSpace(splitted[0]),
			Value:      strings.TrimSpace(splitted[1]),
			Operator:   svccore.ListFilter_NOT_LIKE,
			IsMetadata: true,
		}
	}
	return nil
}

func resourceStateLabel(state svclookup.StatusResponse_State) string {
	switch state {
	case svclookup.StatusResponse_OK:
		return msg.Green("OK").String()
	case svclookup.StatusResponse_ADDING:
		return msg.Cyan("ADDING").String()
	case svclookup.StatusResponse_REMOVING:
		return msg.Cyan("REMOVING").String()
	case svclookup.StatusResponse_ERRORED:
		return msg.Red("ERRORED").String()
	}
	return msg.Yellow("Unknown").String()
}

func confirmChanges(title string, confirm *bool) error {
	field := huh.NewConfirm().Title(title).Value(confirm)
	return huh.NewForm(huh.NewGroup(field)).WithTheme(tuicommon.FormTheme).WithShowHelp(false).Run()
}
