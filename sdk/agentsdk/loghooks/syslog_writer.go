package loghooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/leodido/go-syslog/v4"
	"github.com/leodido/go-syslog/v4/rfc5424"
	"github.com/rs/zerolog"
)

var (
	compID       = "flock"
	regexNewLine = regexp.MustCompile(`(\n|\r\n|\r)`)
)

// SyslogWriter sends every zerolog JSON line it receives to syslog
type SyslogWriter struct {
	writer         io.WriteCloser
	hostname       string
	appName        string
	procID         string
	formatRfc3164  bool
	structuredData map[string]map[string]string
}

func NewSyslogWriter(writer io.WriteCloser, hostname, appName string, formatRfc3164 bool, structuredData map[string]map[string]string) *SyslogWriter {
	return &SyslogWriter{
		writer:         writer,
		hostname:       hostname,
		appName:        appName,
		procID:         strconv.Itoa(os.Getpid()),
		formatRfc3164:  formatRfc3164,
		structuredData: structuredData,
	}
}

func (w *SyslogWriter) Write(line []byte) (int, error) {
	fields := map[string]any{}
	decoder := json.NewDecoder(bytes.NewReader(line))
	// numbers stay as written (no 1e+06 for big integers)
	decoder.UseNumber()
	if err := decoder.Decode(&fields); err != nil {
		return 0, fmt.Errorf("decode log line for syslog: %w", err)
	}
	message := takeString(fields, zerolog.MessageFieldName)
	if message == "" {
		return len(line), nil
	}
	base := w.toBaseMessage(fields)

	var out string
	if w.formatRfc3164 {
		message = regexNewLine.ReplaceAllString(message+fieldsAsText(fields), " ")
		base.Message = &message
		out = baseToRfc3164(base)
	} else {
		structuredData := make(map[string]map[string]string)
		maps.Copy(structuredData, w.structuredData)
		structuredData[compID] = fieldsAsStrings(fields)
		message = regexNewLine.ReplaceAllString(message, " ") + "\n"
		base.Message = &message
		var err error
		out, err = (&rfc5424.SyslogMessage{Base: base, Version: 1, StructuredData: &structuredData}).String()
		if err != nil {
			return 0, fmt.Errorf("convert syslog message to string: %w", err)
		}
	}
	if _, err := w.writer.Write([]byte(out)); err != nil {
		return 0, err
	}
	return len(line), nil
}

// toBaseMessage takes the header fields out of fields, what is left is the data of the line
func (w *SyslogWriter) toBaseMessage(fields map[string]any) syslog.Base {
	facility := uint8(1)
	severity := toSeverity(takeString(fields, zerolog.LevelFieldName))
	priority := facility*8 + severity

	timestamp, err := time.Parse(zerolog.TimeFieldFormat, takeString(fields, zerolog.TimestampFieldName))
	if err != nil {
		timestamp = time.Now()
	}
	msgID := takeString(fields, "msg_id")
	if msgID == "" {
		msgID = "flock-agent-log"
	}
	return syslog.Base{
		Facility:  &facility,
		Severity:  &severity,
		Priority:  &priority,
		Timestamp: &timestamp,
		Hostname:  &w.hostname,
		Appname:   &w.appName,
		ProcID:    &w.procID,
		MsgID:     &msgID,
	}
}

func toSeverity(level string) uint8 {
	switch level {
	case "panic":
		return 1
	case "fatal":
		return 2
	case "error":
		return 3
	case "warn":
		return 4
	case "info":
		return 6
	default:
		return 7
	}
}

func takeString(fields map[string]any, key string) string {
	value, ok := fields[key]
	if !ok {
		return ""
	}
	delete(fields, key)
	return fmt.Sprint(value)
}

func fieldsAsStrings(fields map[string]any) map[string]string {
	asStrings := make(map[string]string, len(fields))
	for key, value := range fields {
		asStrings[key] = fmt.Sprint(value)
	}
	return asStrings
}

// fieldsAsText gives " key=value" for every field, sorted by key
func fieldsAsText(fields map[string]any) string {
	text := strings.Builder{}
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		fmt.Fprintf(&text, " %s=%v", key, fields[key])
	}
	return text.String()
}

func baseToRfc3164(base syslog.Base) string {
	timestamp := base.Timestamp.Format(time.RFC3339)
	return fmt.Sprintf("<%d>%s %s %s[%s]: %s\n",
		*base.Priority, timestamp, *base.Hostname, *base.Appname, *base.ProcID, *base.Message)
}
