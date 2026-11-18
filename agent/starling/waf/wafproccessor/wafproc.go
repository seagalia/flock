package wafproccessor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"

	"github.com/corazawaf/coraza/v3"
	"github.com/corazawaf/coraza/v3/experimental"
	"github.com/corazawaf/coraza/v3/experimental/plugins/plugintypes"
	"github.com/corazawaf/coraza/v3/types"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extProcFilter "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"github.com/rs/zerolog"
)

type WafProc struct {
	tx           types.Transaction
	interruption *types.Interruption
	protocol     string
	logger       *zerolog.Logger

	// In BUFFERED_PARTIAL mode a body over envoy's buffer limit comes as one message with end_of_stream false and
	// nothing after it, so that message is the last one the WAF sees.
	requestBodyInOneMessage  bool
	responseBodyInOneMessage bool
}

func NewWafProc(ctx context.Context, waf coraza.WAF, logger *zerolog.Logger) *WafProc {
	ctxWaf := waf.(experimental.WAFWithOptions)
	tx := ctxWaf.NewTransactionWithOptions(experimental.Options{
		Context: ctx,
	})
	return &WafProc{
		tx:     tx,
		logger: logger,
	}
}

func (w *WafProc) SetProtocolConfig(config *extProcPb.ProtocolConfiguration) {
	w.requestBodyInOneMessage = isBufferedMode(config.GetRequestBodyMode())
	w.responseBodyInOneMessage = isBufferedMode(config.GetResponseBodyMode())
}

func (w *WafProc) ProcessRequestHeader(req *extProcPb.ProcessingRequest_RequestHeaders, attrs map[string]any) error {
	if w.tx.IsRuleEngineOff() {
		return nil
	}

	err := w.processConnection(attrs)
	if err != nil {
		return fmt.Errorf("failed to process connection: %w", err)
	}

	err = w.processURI(attrs)
	if err != nil {
		return fmt.Errorf("failed to process URI: %w", err)
	}

	headers := req.RequestHeaders.GetHeaders().GetHeaders()
	for _, header := range headers {
		w.processHeader(header, false)
	}

	w.interruption = w.tx.ProcessRequestHeaders()

	if w.interruption == nil && req.RequestHeaders.GetEndOfStream() {
		inter, err := w.tx.ProcessRequestBody()
		if err != nil {
			return fmt.Errorf("failed to process request body: %w", err)
		}
		w.interruption = inter
	}

	return nil
}

func (w *WafProc) ProcessRequestBody(req *extProcPb.ProcessingRequest_RequestBody) error {
	if w.tx.IsRuleEngineOff() || w.interruption != nil {
		return nil
	}

	// A buffered body comes whole in one message. Without end_of_stream, envoy kept the rest of it for the backend:
	// the route has partial_request_body and the body is over its request_body_limit.
	isTruncatedByEnvoy := w.requestBodyInOneMessage && !req.RequestBody.GetEndOfStream()
	if isTruncatedByEnvoy {
		w.markBodyTruncated()
	}

	inter, _, err := w.tx.WriteRequestBody(req.RequestBody.GetBody())
	if err != nil {
		return fmt.Errorf("failed to write request body: %w", err)
	}
	if inter != nil {
		w.interruption = inter
		return nil
	}

	isLastMessage := req.RequestBody.GetEndOfStream() || w.requestBodyInOneMessage
	if !isLastMessage {
		return nil
	}
	inter, err = w.tx.ProcessRequestBody()
	if err != nil {
		return fmt.Errorf("failed to process request body: %w", err)
	}
	w.interruption = inter
	return nil
}

// markBodyTruncated lets rule 9000003 (truncatedBodyDirectives) turn off the parse error rules for this request, since
// a truncated JSON or multipart body never parses. The mark is INBOUND_DATA_ERROR, the variable coraza itself sets for
// a body over its own limit. Coraza has no public setter for it, so the variable is set through its concrete type.
func (w *WafProc) markBodyTruncated() {
	inboundDataError := w.tx.(plugintypes.TransactionState).Variables().InboundDataError()
	settable, ok := inboundDataError.(interface{ Set(string) })
	if !ok {
		w.logger.Error().Msg("coraza no longer lets the WAF set INBOUND_DATA_ERROR, truncated bodies are refused")
		return
	}
	settable.Set("1")
}

func (w *WafProc) ProcessResponseHeader(req *extProcPb.ProcessingRequest_ResponseHeaders) error {
	if w.tx.IsRuleEngineOff() || w.interruption != nil {
		return nil
	}

	headers := req.ResponseHeaders.GetHeaders().GetHeaders()
	for _, header := range headers {
		w.processHeader(header, true)
	}

	indexStatus := slices.IndexFunc(headers, func(h *corev3.HeaderValue) bool {
		return h.Key == ":status"
	})
	if indexStatus == -1 {
		return fmt.Errorf("status header not found")
	}
	statusRaw := getHeaderValue(headers[indexStatus])
	status, err := strconv.Atoi(statusRaw)
	if err != nil {
		return fmt.Errorf("invalid status: %w", err)
	}

	w.interruption = w.tx.ProcessResponseHeaders(status, w.protocol)

	if w.interruption == nil && req.ResponseHeaders.GetEndOfStream() {
		inter, err := w.tx.ProcessResponseBody()
		if err != nil {
			return fmt.Errorf("failed to process response body: %w", err)
		}
		w.interruption = inter
	}

	return nil
}

func (w *WafProc) ProcessResponseBody(req *extProcPb.ProcessingRequest_ResponseBody) error {
	if w.tx.IsRuleEngineOff() || w.interruption != nil {
		return nil
	}

	if w.tx.IsResponseBodyProcessable() {
		inter, _, err := w.tx.WriteResponseBody(req.ResponseBody.GetBody())
		if err != nil {
			return fmt.Errorf("failed to write response body: %w", err)
		}
		if inter != nil {
			w.interruption = inter
			return nil
		}
	}

	isLastMessage := req.ResponseBody.GetEndOfStream() || w.responseBodyInOneMessage
	if !isLastMessage {
		return nil
	}
	inter, err := w.tx.ProcessResponseBody()
	if err != nil {
		return fmt.Errorf("failed to process response body: %w", err)
	}
	w.interruption = inter
	return nil
}

func (w *WafProc) ResponseInterruption() (*extProcPb.ProcessingResponse_ImmediateResponse, bool) {
	if w.interruption == nil {
		return nil, false
	}

	statusCode := w.interruption.Status
	isRedirect := w.interruption.Action == "redirect"
	isRedirectionStatus := statusCode >= 300 && statusCode < 400
	switch {
	case isRedirect && !isRedirectionStatus:
		statusCode = http.StatusFound
	case statusCode == 0:
		statusCode = http.StatusForbidden
	}
	immediateResponse := &extProcPb.ImmediateResponse{
		Status: &typev3.HttpStatus{
			Code: typev3.StatusCode(statusCode),
		},
		Body: []byte(http.StatusText(statusCode)),
	}
	if isRedirect {
		immediateResponse.Headers = &extProcPb.HeaderMutation{
			SetHeaders: []*corev3.HeaderValueOption{{
				Header: &corev3.HeaderValue{
					Key:      "Location",
					RawValue: []byte(w.interruption.Data),
				},
			}},
		}
	}
	return &extProcPb.ProcessingResponse_ImmediateResponse{
		ImmediateResponse: immediateResponse,
	}, true
}

func (w *WafProc) processHeader(headerVal *corev3.HeaderValue, forResponse bool) {
	// avoid header like :status, :scheme given by envoy.
	if headerVal.GetKey()[0] == ':' {
		return
	}
	val := getHeaderValue(headerVal)
	if forResponse {
		w.tx.AddResponseHeader(headerVal.GetKey(), val)
	} else {
		w.tx.AddRequestHeader(headerVal.GetKey(), val)
	}
}

func (w *WafProc) processURI(attrs map[string]any) error {
	host, ok := attrs["request.host"].(string)
	if !ok {
		return errors.New("failed to get host")
	}
	path, ok := attrs["request.path"].(string)
	if !ok {
		return errors.New("failed to get path")
	}
	method, ok := attrs["request.method"].(string)
	if !ok {
		return errors.New("failed to get method")
	}
	protocol, ok := attrs["request.protocol"].(string)
	if !ok {
		return errors.New("failed to get protocol")
	}
	w.protocol = protocol

	// Rules expect REQUEST_URI to start with the path, as on an origin server, so the host stays out of it.
	w.tx.ProcessURI(path, method, protocol)

	w.tx.AddRequestHeader("Host", host)
	w.tx.SetServerName(host)
	return nil
}

func (w *WafProc) processConnection(attrs map[string]any) error {
	clientAddr, ok := attrs["source.address"].(string)
	if !ok {
		return errors.New("failed to get source address")
	}

	clientPort, ok := attrs["source.port"].(float64)
	if !ok {
		return errors.New("failed to get source port")
	}

	serverAddr, ok := attrs["destination.address"].(string)
	if !ok {
		return errors.New("failed to get destination address")
	}

	serverPort, ok := attrs["destination.port"].(float64)
	if !ok {
		return errors.New("failed to get destination port")
	}

	w.tx.ProcessConnection(addressHost(clientAddr), int(clientPort), addressHost(serverAddr), int(serverPort))

	return nil
}

func (w *WafProc) Close() error {
	w.tx.ProcessLogging()
	logTransaction(w.logger, w.tx)
	return w.tx.Close()
}

func getHeaderValue(headerVal *corev3.HeaderValue) string {
	val := headerVal.GetValue()
	if val == "" {
		val = string(headerVal.GetRawValue())
	}
	return val
}

func addressHost(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return address
	}
	return host
}

func isBufferedMode(mode extProcFilter.ProcessingMode_BodySendMode) bool {
	return mode == extProcFilter.ProcessingMode_BUFFERED || mode == extProcFilter.ProcessingMode_BUFFERED_PARTIAL
}
