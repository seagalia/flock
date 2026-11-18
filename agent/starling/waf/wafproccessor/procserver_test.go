package wafproccessor

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extProcFilter "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/rs/zerolog"
	svcregister "github.com/seagalia/flock/sdk/starling_waf/services/register/v1"
	"golang.org/x/sync/semaphore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	sqlInjection = "1' OR '1'='1"
	mysqlError   = "You have an error in your SQL syntax; check the manual that corresponds to your MySQL server " +
		"version for the right syntax to use near ''' at line 1"
)

var _ = Describe("EnvoyProcServer", func() {
	Context("with the default CRS WAF", func() {
		It("lets a clean request and its response through", func() {
			exchange := openExchange("")

			resp := exchange.mustSend(requestHeaders(http.MethodGet, "/index.html", nil, true))
			Expect(resp.GetRequestHeaders()).ToNot(BeNil())
			resp = exchange.mustSend(responseHeaders(http.StatusOK, false))
			Expect(resp.GetResponseHeaders()).ToNot(BeNil())
			resp = exchange.mustSend(responseBody("<html><body>hello</body></html>", true))
			Expect(resp.GetResponseBody()).ToNot(BeNil())
		})

		It("blocks an SQL injection in the query string on the request headers", func() {
			exchange := openExchange("")

			resp := exchange.mustSend(requestHeaders(http.MethodGet, "/?id=1%27%20OR%20%271%27%3D%271", nil, true))
			Expect(immediateStatus(resp)).To(Equal(http.StatusForbidden))
		})

		It("blocks an SQL injection in a form body once the body ends", func() {
			exchange := openExchange("")
			formHeader := map[string]string{"content-type": "application/x-www-form-urlencoded"}

			resp := exchange.mustSend(requestHeaders(http.MethodPost, "/login", formHeader, false))
			Expect(resp.GetRequestHeaders()).ToNot(BeNil())
			resp = exchange.mustSend(requestBody("id="+sqlInjection, true))
			Expect(immediateStatus(resp)).To(Equal(http.StatusForbidden))
		})

		It("blocks a response leaking a database error", func() {
			exchange := openExchange("")

			exchange.mustSend(requestHeaders(http.MethodGet, "/search", nil, true))
			exchange.mustSend(responseHeaders(http.StatusOK, false))
			resp := exchange.mustSend(responseBody("<html><body>"+mysqlError+"</body></html>", true))
			Expect(immediateStatus(resp)).To(Equal(http.StatusForbidden))
		})

		It("inspects a partial response body when envoy buffers in BUFFERED_PARTIAL mode", func() {
			exchange := openExchange("")

			headers := requestHeaders(http.MethodGet, "/search", nil, true)
			headers.ProtocolConfig = &extProcPb.ProtocolConfiguration{
				RequestBodyMode:  extProcFilter.ProcessingMode_BUFFERED,
				ResponseBodyMode: extProcFilter.ProcessingMode_BUFFERED_PARTIAL,
			}
			exchange.mustSend(headers)
			exchange.mustSend(responseHeaders(http.StatusOK, false))
			// over envoy's buffer limit, the only body message has end_of_stream false and nothing follows it
			resp := exchange.mustSend(responseBody("<html><body>"+mysqlError, false))
			Expect(immediateStatus(resp)).To(Equal(http.StatusForbidden))
		})

		It("lets through a JSON body envoy cut at the limit of a route with partial_request_body", func() {
			exchange := openExchange("")
			jsonHeader := map[string]string{"content-type": "application/json"}

			headers := requestHeaders(http.MethodPost, "/upload", jsonHeader, false)
			headers.ProtocolConfig = &extProcPb.ProtocolConfiguration{
				RequestBodyMode:  extProcFilter.ProcessingMode_BUFFERED_PARTIAL,
				ResponseBodyMode: extProcFilter.ProcessingMode_BUFFERED_PARTIAL,
			}
			exchange.mustSend(headers)
			// over the limit, envoy sends the first bytes with end_of_stream false and the rest goes to the backend
			resp := exchange.mustSend(requestBody(`{"name": "a file", "content": "aaaa`, false))
			Expect(resp.GetRequestBody()).ToNot(BeNil())
		})

		It("refuses a malformed JSON body envoy sent whole with partial_request_body", func() {
			exchange := openExchange("")
			jsonHeader := map[string]string{"content-type": "application/json"}

			headers := requestHeaders(http.MethodPost, "/upload", jsonHeader, false)
			headers.ProtocolConfig = &extProcPb.ProtocolConfiguration{
				RequestBodyMode:  extProcFilter.ProcessingMode_BUFFERED_PARTIAL,
				ResponseBodyMode: extProcFilter.ProcessingMode_BUFFERED_PARTIAL,
			}
			exchange.mustSend(headers)
			resp := exchange.mustSend(requestBody(`{"name": "a file", "content": "aaaa`, true))
			Expect(immediateStatus(resp)).To(Equal(http.StatusBadRequest))
		})

		It("asks envoy for more time for a request body the CRS can't inspect in 50 ms", func() {
			formHeader := map[string]string{"content-type": "application/x-www-form-urlencoded"}
			bigBody := "a=" + strings.Repeat("a", 200*1024)

			exchange := openExchange("")
			exchange.mustSend(requestHeaders(http.MethodPost, "/upload", formHeader, false))
			expected, _ := requestBodyTimeout(len(bigBody))
			resp := exchange.mustSend(requestBody(bigBody, true))
			Expect(resp.GetRequestBody()).ToNot(BeNil())
			Expect(exchange.overriddenTimeout).To(Equal(expected))

			exchange = openExchange("")
			exchange.mustSend(requestHeaders(http.MethodPost, "/login", formHeader, false))
			exchange.mustSend(requestBody("a=b", true))
			Expect(exchange.overriddenTimeout).To(BeZero())
		})

		It("rejects a form over the arguments limit", func() {
			exchange := openExchange("")
			formHeader := map[string]string{"content-type": "application/x-www-form-urlencoded"}

			exchange.mustSend(requestHeaders(http.MethodPost, "/login", formHeader, false))
			resp := exchange.mustSend(requestBody(formWithFields(129), true))
			Expect(immediateStatus(resp)).To(Equal(http.StatusBadRequest))
		})

		It("counts the blocked requests", func() {
			blockedBefore := testutil.ToFloat64(stats.requests.WithLabelValues("blocked"))
			exchange := openExchange("")

			exchange.mustSend(requestHeaders(http.MethodGet, "/?id=1%27%20OR%20%271%27%3D%271", nil, true))
			Expect(exchange.stream.CloseSend()).To(Succeed())

			// the transaction is counted when the server closes it, after the stream ends
			Eventually(func() float64 {
				return testutil.ToFloat64(stats.requests.WithLabelValues("blocked"))
			}).Should(Equal(blockedBefore + 1))
		})
	})

	Context("with registered WAF configs", func() {
		register := func(req *svcregister.RegisterWafConfigRequest) {
			GinkgoHelper()
			_, err := procServer.RegisterWafConfig(context.Background(), req)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(procServer.UnregisterWafConfig, context.Background(),
				&svcregister.UnregisterWafConfigRequest{Identifier: req.GetIdentifier()})
		}
		registerRules := func(identifier, rules string) {
			GinkgoHelper()
			register(&svcregister.RegisterWafConfigRequest{
				Identifier: identifier,
				Config:     &svcregister.RegisterWafConfigRequest_CustomRules{CustomRules: rules},
			})
		}

		It("fails the stream of an identifier that was never registered", func() {
			exchange := openExchange("never-registered")

			_, err := exchange.send(requestHeaders(http.MethodGet, "/", nil, true))
			Expect(status.Code(err)).To(Equal(codes.Internal))
		})

		It("applies custom rules in every phase", func() {
			registerRules("phases", `
				SecRuleEngine On
				SecRequestBodyAccess On
				SecResponseBodyAccess On
				SecResponseBodyMimeType text/html
				SecRule REQUEST_HEADERS:X-Block "@streq phase1" "id:1001,phase:1,deny,status:403"
				SecRule ARGS_POST:block "@streq phase2" "id:1002,phase:2,deny,status:403"
				SecRule RESPONSE_HEADERS:X-Block "@streq phase3" "id:1003,phase:3,deny,status:403"
				SecRule RESPONSE_BODY "@contains phase4" "id:1004,phase:4,deny,status:403"
			`)
			formHeader := map[string]string{"content-type": "application/x-www-form-urlencoded"}

			exchange := openExchange("phases")
			resp := exchange.mustSend(requestHeaders(http.MethodGet, "/", map[string]string{"x-block": "phase1"}, true))
			Expect(immediateStatus(resp)).To(Equal(http.StatusForbidden))

			exchange = openExchange("phases")
			exchange.mustSend(requestHeaders(http.MethodPost, "/", formHeader, false))
			resp = exchange.mustSend(requestBody("block=phase2", true))
			Expect(immediateStatus(resp)).To(Equal(http.StatusForbidden))

			exchange = openExchange("phases")
			exchange.mustSend(requestHeaders(http.MethodGet, "/", nil, true))
			resp = exchange.mustSend(responseHeadersWith(http.StatusOK, map[string]string{"x-block": "phase3"}))
			Expect(immediateStatus(resp)).To(Equal(http.StatusForbidden))

			exchange = openExchange("phases")
			exchange.mustSend(requestHeaders(http.MethodGet, "/", nil, true))
			exchange.mustSend(responseHeaders(http.StatusOK, false))
			resp = exchange.mustSend(responseBody("<html>phase4</html>", true))
			Expect(immediateStatus(resp)).To(Equal(http.StatusForbidden))

			exchange = openExchange("phases")
			resp = exchange.mustSend(requestHeaders(http.MethodGet, "/", nil, true))
			Expect(resp.GetRequestHeaders()).ToNot(BeNil())
		})

		It("redirects with the status of the rule, or 302 when the rule gives none", func() {
			registerRules("redirects", `
				SecRuleEngine On
				SecRule REQUEST_URI "@beginsWith /moved" "id:2001,phase:1,redirect:https://example.com/new,status:301"
				SecRule REQUEST_URI "@beginsWith /elsewhere" "id:2002,phase:1,redirect:https://example.com/other"
			`)

			resp := openExchange("redirects").mustSend(requestHeaders(http.MethodGet, "/moved", nil, true))
			Expect(immediateStatus(resp)).To(Equal(http.StatusMovedPermanently))
			Expect(resp.GetImmediateResponse().GetHeaders().GetSetHeaders()).To(ContainElement(
				HaveField("Header", HaveField("RawValue", Equal([]byte("https://example.com/new"))))))

			resp = openExchange("redirects").mustSend(requestHeaders(http.MethodGet, "/elsewhere", nil, true))
			Expect(immediateStatus(resp)).To(Equal(http.StatusFound))
		})

		It("only detects with a detect only config", func() {
			register(&svcregister.RegisterWafConfigRequest{
				Identifier: "detect",
				Config:     &svcregister.RegisterWafConfigRequest_DetectOnly{DetectOnly: true},
			})
			detectedBefore := testutil.ToFloat64(stats.requests.WithLabelValues("detected"))
			exchange := openExchange("detect")

			resp := exchange.mustSend(requestHeaders(http.MethodGet, "/?id=1%27%20OR%20%271%27%3D%271", nil, true))
			Expect(resp.GetRequestHeaders()).ToNot(BeNil())
			Expect(exchange.stream.CloseSend()).To(Succeed())

			Eventually(func() float64 {
				return testutil.ToFloat64(stats.requests.WithLabelValues("detected"))
			}).Should(Equal(detectedBefore + 1))
		})

		It("forgets an unregistered config", func() {
			_, err := procServer.RegisterWafConfig(context.Background(), &svcregister.RegisterWafConfigRequest{
				Identifier: "short-lived",
				Config:     &svcregister.RegisterWafConfigRequest_DetectOnly{DetectOnly: true},
			})
			Expect(err).ToNot(HaveOccurred())
			_, err = openExchange("short-lived").send(requestHeaders(http.MethodGet, "/", nil, true))
			Expect(err).ToNot(HaveOccurred())

			_, err = procServer.UnregisterWafConfig(context.Background(),
				&svcregister.UnregisterWafConfigRequest{Identifier: "short-lived"})
			Expect(err).ToNot(HaveOccurred())

			_, err = openExchange("short-lived").send(requestHeaders(http.MethodGet, "/", nil, true))
			Expect(status.Code(err)).To(Equal(codes.Internal))
		})

		It("enforces the limits on custom rules, even when they raise them", func() {
			registerRules("raised-limits", `
				SecRuleEngine On
				SecRequestBodyAccess On
				SecArgumentsLimit 1000
				SecRequestBodyLimit 20000000
			`)
			formHeader := map[string]string{"content-type": "application/x-www-form-urlencoded"}

			resp := openExchange("raised-limits").mustSend(requestHeaders(http.MethodGet, "/?"+formWithFields(129), nil, true))
			Expect(immediateStatus(resp)).To(Equal(http.StatusBadRequest))

			exchange := openExchange("raised-limits")
			exchange.mustSend(requestHeaders(http.MethodPost, "/login", formHeader, false))
			resp = exchange.mustSend(requestBody(formWithFields(129), true))
			Expect(immediateStatus(resp)).To(Equal(http.StatusBadRequest))

			exchange = openExchange("raised-limits")
			exchange.mustSend(requestHeaders(http.MethodPost, "/upload", formHeader, false))
			resp = exchange.mustSend(requestBody("a="+strings.Repeat("a", 9*1024*1024), true))
			Expect(immediateStatus(resp)).To(Equal(http.StatusRequestEntityTooLarge))

			exchange = openExchange("raised-limits")
			exchange.mustSend(requestHeaders(http.MethodPost, "/login", formHeader, false))
			resp = exchange.mustSend(requestBody(formWithFields(128), true))
			Expect(resp.GetRequestBody()).ToNot(BeNil())
		})

		It("refuses custom rules coraza can't compile", func() {
			_, err := procServer.RegisterWafConfig(context.Background(), &svcregister.RegisterWafConfigRequest{
				Identifier: "broken",
				Config:     &svcregister.RegisterWafConfigRequest_CustomRules{CustomRules: "SecRule NOT_A_VARIABLE"},
			})
			Expect(status.Code(err)).To(Equal(codes.Internal))
		})
	})
})

var _ = Describe("requestBodyTimeout", func() {
	It("asks for more time only when the estimate is over a quarter of the message timeout", func() {
		quarterSize := int(envoyMessageTimeout / 4 * 1024 / requestBodyCostPerKiB)

		_, isBig := requestBodyTimeout(quarterSize)
		Expect(isBig).To(BeFalse())
		timeout, isBig := requestBodyTimeout(2 * quarterSize)
		Expect(isBig).To(BeTrue())
		Expect(timeout).To(Equal(envoyMessageTimeout + envoyMessageTimeout/2))
		timeout, _ = requestBodyTimeout(1 << 30)
		Expect(timeout).To(Equal(envoyMaxMessageTimeout))
	})
})

var _ = Describe("stream limit", func() {
	It("refuses a stream once the WAF holds as many as its memory allows", func() {
		server := &EnvoyProcServer{entry: zerolog.Nop(), streams: semaphore.NewWeighted(0)}
		refusedBefore := testutil.ToFloat64(stats.requests.WithLabelValues("refused"))

		err := server.Process(nil)
		Expect(status.Code(err)).To(Equal(codes.ResourceExhausted))
		Expect(testutil.ToFloat64(stats.requests.WithLabelValues("refused"))).To(Equal(refusedBefore + 1))
	})

	DescribeTable("maxStreams",
		func(memory uint64, cores int, expected int64) {
			Expect(maxStreams(memory, cores)).To(Equal(expected))
		},
		Entry("is bound by the memory", uint64(1<<30), 8, int64(512)),
		Entry("is bound by the cores", uint64(16<<30), 2, int64(400)),
		Entry("keeps one stream on a tiny memory", uint64(1<<20), 4, int64(1)),
	)
})

var _ = Describe("evaluate", func() {
	It("keeps the cores the big request bodies don't hold for the other messages", func() {
		server := &EnvoyProcServer{
			evaluations:        semaphore.NewWeighted(2),
			bigBodyEvaluations: semaphore.NewWeighted(1),
		}
		Expect(server.bigBodyEvaluations.Acquire(context.Background(), 1)).To(Succeed())
		processed := 0
		process := func() error {
			processed++
			return nil
		}

		Expect(server.evaluate(context.Background(), false, process)).To(Succeed())
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		err := server.evaluate(ctx, true, process)
		Expect(status.Code(err)).To(Equal(codes.DeadlineExceeded))
		Expect(processed).To(Equal(1))
	})
})

func formWithFields(fields int) string {
	pairs := make([]string, fields)
	for i := range pairs {
		pairs[i] = fmt.Sprintf("field%d=value", i)
	}
	return strings.Join(pairs, "&")
}

// requestHeaders is the first message of a stream, with the attributes envoy.yaml asks envoy to send.
func requestHeaders(method, path string, headers map[string]string, endOfStream bool) *extProcPb.ProcessingRequest {
	attrs, err := structpb.NewStruct(map[string]any{
		"source.address":      "192.0.2.10:51000",
		"source.port":         51000,
		"destination.address": "192.0.2.1:8080",
		"destination.port":    8080,
		"request.scheme":      "http",
		"request.host":        "example.com",
		"request.path":        path,
		"request.method":      method,
		"request.protocol":    "HTTP/1.1",
	})
	Expect(err).ToNot(HaveOccurred())
	allHeaders := map[string]string{
		":method":    method,
		":path":      path,
		":authority": "example.com",
		"user-agent": "Mozilla/5.0 (X11; Linux x86_64) starling-waf-spec",
		"accept":     "text/html",
	}
	maps.Copy(allHeaders, headers)
	return &extProcPb.ProcessingRequest{
		Request: &extProcPb.ProcessingRequest_RequestHeaders{RequestHeaders: &extProcPb.HttpHeaders{
			Headers:     headerMap(allHeaders),
			EndOfStream: endOfStream,
		}},
		Attributes: map[string]*structpb.Struct{"envoy.filters.http.ext_proc": attrs},
	}
}

func requestBody(body string, endOfStream bool) *extProcPb.ProcessingRequest {
	return &extProcPb.ProcessingRequest{
		Request: &extProcPb.ProcessingRequest_RequestBody{RequestBody: &extProcPb.HttpBody{
			Body:        []byte(body),
			EndOfStream: endOfStream,
		}},
	}
}

func responseHeaders(statusCode int, endOfStream bool) *extProcPb.ProcessingRequest { //nolint:unparam // the call sites show the value
	req := responseHeadersWith(statusCode, nil)
	req.GetResponseHeaders().EndOfStream = endOfStream
	return req
}

func responseHeadersWith(statusCode int, headers map[string]string) *extProcPb.ProcessingRequest {
	allHeaders := map[string]string{":status": strconv.Itoa(statusCode), "content-type": "text/html"}
	maps.Copy(allHeaders, headers)
	return &extProcPb.ProcessingRequest{
		Request: &extProcPb.ProcessingRequest_ResponseHeaders{ResponseHeaders: &extProcPb.HttpHeaders{
			Headers: headerMap(allHeaders),
		}},
	}
}

func responseBody(body string, endOfStream bool) *extProcPb.ProcessingRequest {
	return &extProcPb.ProcessingRequest{
		Request: &extProcPb.ProcessingRequest_ResponseBody{ResponseBody: &extProcPb.HttpBody{
			Body:        []byte(body),
			EndOfStream: endOfStream,
		}},
	}
}

// headerMap sends raw values like envoy does.
func headerMap(headers map[string]string) *corev3.HeaderMap {
	headerMap := &corev3.HeaderMap{}
	for key, value := range headers {
		headerMap.Headers = append(headerMap.Headers, &corev3.HeaderValue{Key: key, RawValue: []byte(value)})
	}
	return headerMap
}
