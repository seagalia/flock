package rpcserver

import (
	"errors"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	flocklog "github.com/seagalia/flock/sdk/logging"
)

// healthService is polled continuously, only its failures are worth a log line
const healthService = "grpc.health.v1.Health"

var (
	streamLogger = flocklog.Component("grpc_server_stream")
	unaryLogger  = flocklog.Component("grpc_unary_unary")
)

func loggerFor(streamType connect.StreamType) *zerolog.Logger {
	if streamType == connect.StreamTypeUnary {
		return &unaryLogger
	}
	return &streamLogger
}

type callLog struct {
	logger  *zerolog.Logger
	service string
	fields  map[string]any
	start   time.Time
}

func startCallLog(procedure string, streamType connect.StreamType, peerAddr, protocol string) *callLog {
	service, method := splitProcedure(procedure)
	return &callLog{
		logger:  loggerFor(streamType),
		service: service,
		start:   time.Now(),
		fields: map[string]any{
			"grpc.component":   "server",
			"grpc.service":     service,
			"grpc.method":      method,
			"grpc.method_type": grpcType(streamType),
			"peer.address":     peerAddr,
			"protocol":         protocol,
		},
	}
}

func (c *callLog) finish(err error) {
	code := grpcCode(err)
	if c.service == healthService && code == codes.OK {
		return
	}
	// the gRPC name of the code ("NotFound"), as in metrics labels
	c.fields["grpc.code"] = code.String()
	c.fields["grpc.time_ms"] = float64(time.Since(c.start).Microseconds()) / 1000
	if err != nil {
		c.fields["grpc.error"] = err.Error()
	}
	c.logger.WithLevel(levelOf(err)).Fields(c.fields).Msg("finished call")
}

// levelOf keeps client mistakes at info and server failures at error, as grpc-ecosystem logging does
func levelOf(err error) zerolog.Level {
	switch grpcCode(err) {
	case codes.OK, codes.Canceled, codes.InvalidArgument, codes.NotFound, codes.AlreadyExists, codes.Unauthenticated:
		return zerolog.InfoLevel
	case codes.DeadlineExceeded, codes.PermissionDenied, codes.ResourceExhausted, codes.FailedPrecondition,
		codes.Aborted, codes.OutOfRange, codes.Unavailable:
		return zerolog.WarnLevel
	default:
		return zerolog.ErrorLevel
	}
}

// grpcCode reads the code of an error from the connect server or from the grpc-go one,
// connect and gRPC share the same code numbers
func grpcCode(err error) codes.Code {
	if err == nil {
		return codes.OK
	}
	if connectErr, ok := errors.AsType[*connect.Error](err); ok {
		return codes.Code(connectErr.Code())
	}
	return status.Code(err)
}

func grpcType(streamType connect.StreamType) string {
	switch streamType {
	case connect.StreamTypeClient:
		return "client_stream"
	case connect.StreamTypeServer:
		return "server_stream"
	case connect.StreamTypeBidi:
		return "bidi_stream"
	default:
		return "unary"
	}
}

// splitProcedure splits "/package.Service/Method" into "package.Service" and "Method"
func splitProcedure(procedure string) (string, string) {
	service, method, _ := strings.Cut(strings.TrimPrefix(procedure, "/"), "/")
	return service, method
}

// callMetrics has the names and labels of the grpc-ecosystem prometheus provider, the ones dashboards query
type callMetrics struct {
	started  *prometheus.CounterVec
	handled  *prometheus.CounterVec
	received *prometheus.CounterVec
	sent     *prometheus.CounterVec
}

// callLabels are the values of the grpc_type, grpc_service and grpc_method labels
type callLabels []string

// registered once for the process, as several servers can be created in the same process (tests)
var metrics = newCallMetrics()

func init() {
	prometheus.MustRegister(metrics.started, metrics.handled, metrics.received, metrics.sent)
}

func newCallMetrics() *callMetrics {
	callLabelNames := []string{"grpc_type", "grpc_service", "grpc_method"}
	return &callMetrics{
		started: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "grpc_server_started_total",
			Help: "Total number of RPCs started on the server.",
		}, callLabelNames),
		handled: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "grpc_server_handled_total",
			Help: "Total number of RPCs completed on the server, regardless of success or failure.",
		}, slices.Concat(callLabelNames, []string{"grpc_code"})),
		received: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "grpc_server_msg_received_total",
			Help: "Total number of RPC stream messages received on the server.",
		}, callLabelNames),
		sent: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "grpc_server_msg_sent_total",
			Help: "Total number of gRPC stream messages sent by the server.",
		}, callLabelNames),
	}
}

func labelsOf(procedure string, streamType connect.StreamType) callLabels {
	service, method := splitProcedure(procedure)
	return callLabels{grpcType(streamType), service, method}
}

// initialize creates the series of a method at zero
func (m *callMetrics) initialize(procedure string, streamType connect.StreamType) {
	labels := labelsOf(procedure, streamType)
	m.started.WithLabelValues(labels...)
	m.received.WithLabelValues(labels...)
	m.sent.WithLabelValues(labels...)
	for code := codes.OK; code <= codes.Unauthenticated; code++ {
		m.handled.WithLabelValues(append(labels, code.String())...)
	}
}

func (m *callMetrics) start(procedure string, streamType connect.StreamType) callLabels {
	labels := labelsOf(procedure, streamType)
	m.started.WithLabelValues(labels...).Inc()
	return labels
}

func (m *callMetrics) finish(labels callLabels, err error) {
	m.handled.WithLabelValues(append(labels, grpcCode(err).String())...).Inc()
}

func (m *callMetrics) onReceived(labels callLabels) {
	m.received.WithLabelValues(labels...).Inc()
}

func (m *callMetrics) onSent(labels callLabels) {
	m.sent.WithLabelValues(labels...).Inc()
}
