package as3

import (
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	typev1 "github.com/seagalia/flock/sdk/flock/type/v1"
	"google.golang.org/protobuf/types/known/durationpb"
)

// renderMonitor turns the health check of a backend into an F5 monitor, nil when the backend has no health check.
// It gives the warnings of the approximations it made.
func renderMonitor(b *backend.Backend) (*Monitor, []string) {
	hc := b.GetHealthCheck()
	if hc == nil || hc.GetNoHealthCheck() != nil {
		return nil, nil
	}
	var warnings []string
	m := &Monitor{Class: classMonitor, Remark: remark(b.GetName()), TargetPort: hc.GetAltPort()}
	setTimings(m, hc)
	if hc.GetUnhealthyInterval() != nil {
		warnings = append(warnings, "health check unhealthy_interval has no F5 equivalent, it is ignored")
	}

	switch {
	case hc.GetHttpHealthCheck() != nil:
		warnings = append(warnings, setHttpCheck(m, hc.GetHttpHealthCheck(), hc.GetEnableTls())...)
	case hc.GetTcpHealthCheck() != nil:
		m.MonitorType = monitorTypeTCP
		if hc.GetEnableTls() {
			warnings = append(warnings, "F5 has no tcp monitor over TLS, the tcp check runs without TLS")
		}
		warnings = append(warnings, setPayloads(m, hc.GetTcpHealthCheck().GetSend(), hc.GetTcpHealthCheck().GetReceive())...)
	case hc.GetUdpHealthCheck() != nil:
		m.MonitorType = monitorTypeUDP
		warnings = append(warnings, setPayloads(m, hc.GetUdpHealthCheck().GetSend(), hc.GetUdpHealthCheck().GetReceive())...)
	case hc.GetGrpcHealthCheck() != nil:
		m.MonitorType = monitorTypeTCP
		warnings = append(warnings, "grpc health check has no F5 monitor, a tcp connect check is used")
	case hc.GetPluginHealthCheck() != nil:
		m.MonitorType = monitorTypeTCP
		warnings = append(warnings, "plugin health check has no F5 monitor, a tcp connect check is used")
	}
	return m, warnings
}

// setTimings reproduces the flock thresholds: F5 marks a member down when no check succeeded during timeout, so
// waiting unhealthy_threshold intervals plus the time of one check gives the same reaction as flock.
func setTimings(m *Monitor, hc *core.HealthCheck) {
	unhealthy := uint64(max(hc.GetUnhealthyThreshold(), 1))
	healthy := uint64(max(hc.GetHealthyThreshold(), 1))
	m.Interval = seconds(hc.GetInterval())
	m.Timeout = m.Interval*unhealthy + seconds(hc.GetTimeout())
	m.TimeUntilUp = m.Interval * (healthy - 1)
}

// seconds rounds a duration up to whole seconds, at least one: F5 monitor timings are integers.
func seconds(d *durationpb.Duration) uint64 {
	secs := math.Ceil(d.AsDuration().Seconds())
	return uint64(max(secs, 1))
}

func secondsOrZero(d *durationpb.Duration) uint64 {
	if d == nil || d.AsDuration() <= 0 {
		return 0
	}
	return uint64(math.Ceil(d.AsDuration().Seconds()))
}

func setHttpCheck(m *Monitor, check *core.HttpHealthCheck, enableTls bool) []string {
	var warnings []string
	m.MonitorType = monitorTypeHTTP
	if enableTls {
		m.MonitorType = monitorTypeHTTPS
	}
	if check.GetCodecClientType() != typev1.CodecClientType_HTTP1 {
		warnings = append(warnings, fmt.Sprintf("health check codec %s has no F5 monitor, HTTP/1.1 is used", check.GetCodecClientType()))
	}
	body, ok := payloadText(check.GetSend())
	if !ok {
		warnings = append(warnings, binaryPayloadWarning)
	}
	expectedBody, ok := payloadText(check.GetReceive())
	if !ok {
		warnings = append(warnings, binaryPayloadWarning)
	}

	m.Send = httpRequest(check, body)
	if check.GetHost() == "" {
		warnings = append(warnings, "health check host is empty, F5 sends HTTP/1.0 without a Host header")
	}
	m.Receive = `^HTTP/1\.[01] (` + statusPattern(check.GetExpectedStatuses()) + `)`
	if expectedBody != "" {
		m.Receive += `(.|\r|\n)*` + regexp.QuoteMeta(expectedBody)
	}
	return warnings
}

// httpRequest is the request an F5 http monitor sends verbatim. One monitor serves every member, so without a host
// it can't name each endpoint as flock does: it then speaks HTTP/1.0, which needs no Host header.
func httpRequest(check *core.HttpHealthCheck, body string) string {
	method := http.MethodGet
	if check.GetMethod() != core.RequestMethod_METHOD_UNSPECIFIED {
		method = check.GetMethod().String()
	}
	path := check.GetPath()
	if path == "" {
		path = "/"
	}
	var req strings.Builder
	hasHost := check.GetHost() != ""
	if hasHost {
		fmt.Fprintf(&req, "%s %s HTTP/1.1\r\nHost: %s\r\n", method, path, check.GetHost())
	} else {
		fmt.Fprintf(&req, "%s %s HTTP/1.0\r\n", method, path)
	}
	for _, header := range check.GetRequestHeadersToAdd() {
		fmt.Fprintf(&req, "%s: %s\r\n", header.GetHeader().GetKey(), header.GetHeader().GetValue())
	}
	if body != "" {
		fmt.Fprintf(&req, "Content-Length: %d\r\n", len(body))
	}
	if hasHost {
		req.WriteString("Connection: close\r\n")
	}
	req.WriteString("\r\n")
	req.WriteString(body)
	return req.String()
}

// statusPattern matches the statuses of the half-open range, [200, 300) by default, with whole hundreds and whole
// decades written as digit classes to keep the pattern short.
func statusPattern(statuses *typev1.Int64Range) string {
	start, end := int64(200), int64(300)
	if statuses != nil && statuses.GetEnd() > statuses.GetStart() {
		start, end = statuses.GetStart(), statuses.GetEnd()
	}
	var parts []string
	for code := start; code < end; {
		switch {
		case code%100 == 0 && code+100 <= end:
			parts = append(parts, strconv.FormatInt(code/100, 10)+"[0-9][0-9]")
			code += 100
		case code%10 == 0 && code+10 <= end:
			parts = append(parts, strconv.FormatInt(code/10, 10)+"[0-9]")
			code += 10
		default:
			parts = append(parts, strconv.FormatInt(code, 10))
			code++
		}
	}
	return strings.Join(parts, "|")
}

const binaryPayloadWarning = "binary health check payloads can't be sent by an F5 monitor, the payload is left out"

func setPayloads(m *Monitor, send *core.HealthCheckPayload, receive []*core.HealthCheckPayload) []string {
	var warnings []string
	sendText, ok := payloadText(send)
	if !ok {
		warnings = append(warnings, binaryPayloadWarning)
	}
	m.Send = sendText
	var expected strings.Builder
	for _, block := range receive {
		blockText, ok := payloadText(block)
		if !ok {
			// a partial expectation would match other answers, the whole receive check is dropped
			warnings = append(warnings, binaryPayloadWarning)
			expected.Reset()
			break
		}
		expected.WriteString(blockText)
	}
	if expected.Len() > 0 {
		m.Receive = "^" + regexp.QuoteMeta(expected.String())
	}
	return warnings
}

// payloadText gives a payload as F5 can send it, false when it is binary and must be left out: F5 send strings are
// text, NUL bytes and other control characters can't go through.
func payloadText(p *core.HealthCheckPayload) (string, bool) {
	if p == nil {
		return "", true
	}
	raw := p.GetText()
	if p.GetBinary() != nil {
		raw = string(p.GetBinary())
	}
	if !utf8.ValidString(raw) {
		return "", false
	}
	for _, c := range raw {
		isAllowedControl := c == '\r' || c == '\n' || c == '\t'
		if unicode.IsControl(c) && !isAllowedControl {
			return "", false
		}
	}
	return raw, true
}
