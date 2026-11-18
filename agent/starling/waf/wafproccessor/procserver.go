package wafproccessor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"
	"time"

	corazaconf "github.com/corazawaf/coraza-coreruleset/coraza/v3"
	"github.com/corazawaf/coraza-coreruleset/crs/v4"
	crslts "github.com/corazawaf/coraza-coreruleset/lts/v4"
	"github.com/corazawaf/coraza/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/jcchavezs/mergefs"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/sdk/logging"
	svcregister "github.com/seagalia/flock/sdk/starling_waf/services/register/v1"
	"golang.org/x/sync/semaphore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	MetadataKeyWafUUID     = "x-waf-uuid"
	envoyMessageTimeout    = 200 * time.Millisecond
	envoyMaxMessageTimeout = 10 * time.Second

	// A transaction holds about 1 MB while in flight once the WAF limits apply, and the Go heap grows to twice the
	// live data before collecting with the default GOGC.
	memoryPerStream = 2 << 20
	// CPU of a small request, the bigger ones are rare and bounded by the WAF limits.
	cpuPerStream = time.Millisecond
)

type EnvoyProcServer struct {
	entry                zerolog.Logger
	txlog                zerolog.Logger
	defaultOnWaf         coraza.WAF
	defaultDetectOnlyWaf coraza.WAF

	ephemeralWaf map[string]coraza.WAF
	rwMutex      *sync.RWMutex

	// Bounds the coraza evaluations running at once. Past the number of cores they only queue, and a queued one whose
	// stream envoy gave up on leaves the queue instead of burning CPU for a 504.
	evaluations *semaphore.Weighted
	// Bounds the evaluations of big request bodies to half the cores, so seconds long uploads can't hold every core
	// while the small requests behind them reach their 200 ms timeout.
	bigBodyEvaluations *semaphore.Weighted
	// Bounds the streams held at once, each one holding a coraza transaction for the whole request. Past it a stream
	// is refused at once instead of piling up until the WAF runs out of memory.
	streams *semaphore.Weighted

	extProcPb.UnimplementedExternalProcessorServer
	svcregister.UnimplementedRegisterServer
}

// NewEnvoyProcServer holds as many streams at once as memory allows, capped by the number its cores answer within
// envoy's message timeout.
func NewEnvoyProcServer(memory uint64) (*EnvoyProcServer, error) {
	defaultOn, err := newWAF(`
	               Include @coraza.conf-recommended
	               SecRuleEngine On
	               Include @crs-setup-lts.conf.example
	               # CRS reports the anomaly scores (rule 980170) of every request that scored by default, only
	               # report the requests over the blocking threshold.
	               SecAction "id:900115,phase:1,pass,t:none,nolog,setvar:tx.reporting_level=1"
	               Include @owasp_crs_lts/*.conf
		`)
	if err != nil {
		return nil, fmt.Errorf("error creating default on WAF: %w", err)
	}

	defaultDetectOnly, err := newWAF(`
	               Include @coraza.conf-recommended
	               SecRuleEngine DetectionOnly
	               Include @crs-setup-lts.conf.example
	               # CRS reports the anomaly scores (rule 980170) of every request that scored by default, only
	               # report the requests over the blocking threshold.
	               SecAction "id:900115,phase:1,pass,t:none,nolog,setvar:tx.reporting_level=1"
	               Include @owasp_crs_lts/*.conf
		`)
	if err != nil {
		return nil, fmt.Errorf("error creating default detect only WAF: %w", err)
	}

	return &EnvoyProcServer{
		entry: logging.Component("envoy_proc_server"),
		txlog: logging.Component("waf").Sample(&zerolog.BurstSampler{
			Burst:       100,
			Period:      time.Second,
			NextSampler: &zerolog.BasicSampler{N: 100},
		}),
		defaultOnWaf:         defaultOn,
		defaultDetectOnlyWaf: defaultDetectOnly,
		ephemeralWaf:         make(map[string]coraza.WAF),
		rwMutex:              &sync.RWMutex{},
		evaluations:          semaphore.NewWeighted(int64(runtime.GOMAXPROCS(0))),
		bigBodyEvaluations:   semaphore.NewWeighted(int64(max(1, runtime.GOMAXPROCS(0)/2))),
		streams:              semaphore.NewWeighted(maxStreams(memory, runtime.GOMAXPROCS(0))),
	}, nil
}

func (s *EnvoyProcServer) Process(stream grpc.BidiStreamingServer[extProcPb.ProcessingRequest, extProcPb.ProcessingResponse]) error {
	s.entry.Debug().Msg("processing request")
	if !s.streams.TryAcquire(1) {
		stats.requests.WithLabelValues("refused").Inc()
		return status.Error(codes.ResourceExhausted, "the WAF holds as many requests as its memory allows")
	}
	defer s.streams.Release(1)

	md, ok := metadata.FromIncomingContext(stream.Context())
	if !ok {
		return status.Error(codes.Internal, "no metadata")
	}
	waf, err := s.findWaf(envoyMetadata(md))
	if err != nil {
		s.entry.Error().Err(err).Msg("error finding WAF")
		return status.Errorf(codes.Internal, "error finding WAF: %v", err)
	}
	wafProc := NewWafProc(stream.Context(), waf, &s.txlog)
	defer func() {
		s.entry.Debug().Msg("processing request done")
		err := wafProc.Close()
		if err != nil {
			s.entry.Error().Err(err).Msg("error closing WAF processor")
		}
	}()
	for {
		req, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return status.Errorf(codes.Unknown, "error receiving request: %v", err)
		}
		if protocolConfig := req.GetProtocolConfig(); protocolConfig != nil {
			wafProc.SetProtocolConfig(protocolConfig)
		}
		var attrs map[string]any
		if attrsStruct, ok := req.GetAttributes()["envoy.filters.http.ext_proc"]; ok {
			attrs = attrsStruct.AsMap()
		}

		bodyTimeout, isBigBody := requestBodyTimeout(len(req.GetRequestBody().GetBody()))
		if isBigBody {
			err := stream.Send(&extProcPb.ProcessingResponse{OverrideMessageTimeout: durationpb.New(bodyTimeout)})
			if err != nil {
				return status.Errorf(codes.Unknown, "error sending the message timeout: %v", err)
			}
		}
		err = s.evaluate(stream.Context(), isBigBody, func() error {
			return s.processMessage(wafProc, req, attrs)
		})
		if err != nil {
			return err
		}

		var resp *extProcPb.ProcessingResponse

		respInter, interrupted := wafProc.ResponseInterruption()
		if interrupted {
			resp = &extProcPb.ProcessingResponse{
				Response: respInter,
			}
		} else {
			resp = s.reqToResp(req)
		}

		if err := stream.Send(resp); err != nil {
			return status.Errorf(codes.Unknown, "error sending response: %v", err)
		}
	}
}

func maxStreams(memory uint64, cores int) int64 {
	memoryBound := memory / memoryPerStream
	cpuBound := uint64(cores) * uint64(envoyMessageTimeout/cpuPerStream)
	return int64(max(1, min(memoryBound, cpuBound)))
}

// requestBodyCostPerKiB is the CRS time per KiB of request body with the regexp of Go, taken at four times the 2.5 ms
// of an Apple M5 core so a slower x64 core stays within it. Too long a timeout only delays the 504 of a stuck WAF, too
// short a one fails a legit body. re2_cgo.go lowers it for the RE2 build.
var requestBodyCostPerKiB = 10 * time.Millisecond

// requestBodyTimeout is the time envoy should wait for a request body the CRS can't inspect within a quarter of its
// message timeout, and false for the other messages.
func requestBodyTimeout(bodySize int) (time.Duration, bool) {
	estimate := time.Duration(bodySize) * requestBodyCostPerKiB / 1024
	if estimate <= envoyMessageTimeout/4 {
		return 0, false
	}
	return min(envoyMessageTimeout+estimate, envoyMaxMessageTimeout), true
}

// evaluate runs process once the server has a core for it, and for a big request body a place among the big ones
// first. A stream envoy gave up on leaves the queue.
func (s *EnvoyProcServer) evaluate(ctx context.Context, isBigBody bool, process func() error) error {
	if isBigBody {
		err := s.bigBodyEvaluations.Acquire(ctx, 1)
		if err != nil {
			return status.FromContextError(err).Err()
		}
		defer s.bigBodyEvaluations.Release(1)
	}
	err := s.evaluations.Acquire(ctx, 1)
	if err != nil {
		return status.FromContextError(err).Err()
	}
	defer s.evaluations.Release(1)
	return process()
}

func (s *EnvoyProcServer) processMessage(wafProc *WafProc, req *extProcPb.ProcessingRequest, attrs map[string]any) error {
	switch procReq := req.Request.(type) {
	case *extProcPb.ProcessingRequest_RequestHeaders:
		s.entry.Debug().Msg("processing request header")
		err := wafProc.ProcessRequestHeader(procReq, attrs)
		if err != nil {
			s.entry.Error().Err(err).Msg("error processing request header")
			return status.Errorf(codes.Unknown, "error processing request header: %v", err)
		}
		s.entry.Debug().Msg("processing request header done")
	case *extProcPb.ProcessingRequest_RequestBody:
		s.entry.Debug().Msg("processing request body")
		err := wafProc.ProcessRequestBody(procReq)
		if err != nil {
			s.entry.Error().Err(err).Msg("error processing request body")
			return status.Errorf(codes.Unknown, "error processing request body: %v", err)
		}
		s.entry.Debug().Msg("processing request body done")
	case *extProcPb.ProcessingRequest_ResponseHeaders:
		s.entry.Debug().Msg("processing response header")
		err := wafProc.ProcessResponseHeader(procReq)
		if err != nil {
			s.entry.Error().Err(err).Msg("error processing response header")
			return status.Errorf(codes.Unknown, "error processing response header: %v", err)
		}
		s.entry.Debug().Msg("processing response header done")
	case *extProcPb.ProcessingRequest_ResponseBody:
		s.entry.Debug().Msg("processing response body")
		err := wafProc.ProcessResponseBody(procReq)
		if err != nil {
			s.entry.Error().Err(err).Msg("error processing response body")
			return status.Errorf(codes.Unknown, "error processing response body: %v", err)
		}
		s.entry.Debug().Msg("processing response body done")
	}
	return nil
}

func (s *EnvoyProcServer) RegisterWafConfig(ctx context.Context, req *svcregister.RegisterWafConfigRequest) (*emptypb.Empty, error) {
	uuid := req.GetIdentifier()
	if req.GetDetectOnly() {
		s.rwMutex.Lock()
		s.ephemeralWaf[uuid] = s.defaultDetectOnlyWaf
		s.rwMutex.Unlock()
		return &emptypb.Empty{}, nil
	}
	waf, err := newWAF(req.GetCustomRules())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error creating waf: %v", err)
	}
	s.rwMutex.Lock()
	s.ephemeralWaf[uuid] = waf
	s.rwMutex.Unlock()
	return &emptypb.Empty{}, nil
}
func (s *EnvoyProcServer) UnregisterWafConfig(ctx context.Context, req *svcregister.UnregisterWafConfigRequest) (*emptypb.Empty, error) {
	uuid := req.GetIdentifier()
	s.rwMutex.Lock()
	defer s.rwMutex.Unlock()
	delete(s.ephemeralWaf, uuid)
	return &emptypb.Empty{}, nil
}

func (s *EnvoyProcServer) findWaf(meta envoyMetadata) (coraza.WAF, error) {
	wafUuid := meta.Get(MetadataKeyWafUUID)
	if wafUuid == "" {
		return s.defaultOnWaf, nil
	}
	s.rwMutex.RLock()
	waf, ok := s.ephemeralWaf[wafUuid]
	s.rwMutex.RUnlock()
	if ok {
		return waf, nil
	}

	return nil, errors.New("waf not found")
}

// limitDirectives bound what one transaction holds, since the circuit breaker of envoy's WAF cluster sizes the WAF
// memory as streams times about 1 MB. Each argument costs about 45 KB held while the request is in flight (CRS
// transforms it for every rule), so 128 arguments per source keep a transaction under about 6 MB. Coraza drops the
// arguments past the limit without inspecting them, so the two rules reject such a request instead. Envoy bounds the
// request body with the request_body_limit of the route (8 MiB at most), the coraza limit sits above it with room for
// the last chunk envoy buffers past its limit, only as a safety net. A response body is buffered up to the 32 KiB of
// the backend buffer of envoy. Rule ids 9000001-9000099 are reserved for flock.
const limitDirectives = `
SecArgumentsLimit 128
SecRule ARGUMENTS_LIMIT_REACHED "@eq 1" "id:9000001,phase:1,t:none,log,deny,status:400,msg:'Argument limit reached (GET/PATH args)'"
SecRule ARGUMENTS_LIMIT_REACHED "@eq 1" "id:9000002,phase:2,t:none,log,deny,status:400,msg:'Argument limit reached (POST args)'"
SecRequestBodyLimit 9437184
SecRequestBodyLimitAction Reject
SecResponseBodyLimit 32768
SecResponseBodyLimitAction ProcessPartial
`

// MaxMessageSize is the largest message the WAF server takes, so a request body up to the coraza limit reaches it with
// room for the rest of its message. grpc-go refuses messages over 4 MiB by default.
const MaxMessageSize = 10 << 20

// truncatedBodyDirectives turn off the parse error rules of the coraza recommended config (200002 for JSON and forms,
// 200003 for multipart) on a body WafProc.markBodyTruncated marked, which would otherwise refuse it with a 400. They
// come before the given directives because a rule only removes the rules that run after it.
const truncatedBodyDirectives = `
SecRule INBOUND_DATA_ERROR "@eq 1" "id:9000003,phase:2,t:none,pass,nolog,ctl:ruleRemoveById=200002,ctl:ruleRemoveById=200003"
`

func newWAF(directives string) (coraza.WAF, error) {
	return coraza.NewWAF(coraza.NewWAFConfig().
		WithDirectives(truncatedBodyDirectives + directives + limitDirectives).
		WithRootFS(mergefs.Merge(crs.FS, corazaconf.FS, crslts.FS)),
	)
}

func (s *EnvoyProcServer) reqToResp(req *extProcPb.ProcessingRequest) *extProcPb.ProcessingResponse {
	resp := &extProcPb.ProcessingResponse{}
	switch req.Request.(type) {
	case *extProcPb.ProcessingRequest_RequestHeaders:
		resp.Response = &extProcPb.ProcessingResponse_RequestHeaders{
			RequestHeaders: &extProcPb.HeadersResponse{},
		}
	case *extProcPb.ProcessingRequest_ResponseHeaders:
		resp.Response = &extProcPb.ProcessingResponse_ResponseHeaders{
			ResponseHeaders: &extProcPb.HeadersResponse{},
		}
	case *extProcPb.ProcessingRequest_ResponseBody:
		resp.Response = &extProcPb.ProcessingResponse_ResponseBody{
			ResponseBody: &extProcPb.BodyResponse{},
		}
	case *extProcPb.ProcessingRequest_RequestBody:
		resp.Response = &extProcPb.ProcessingResponse_RequestBody{
			RequestBody: &extProcPb.BodyResponse{},
		}
	case *extProcPb.ProcessingRequest_RequestTrailers:
		resp.Response = &extProcPb.ProcessingResponse_RequestTrailers{
			RequestTrailers: &extProcPb.TrailersResponse{},
		}
	case *extProcPb.ProcessingRequest_ResponseTrailers:
		resp.Response = &extProcPb.ProcessingResponse_ResponseTrailers{
			ResponseTrailers: &extProcPb.TrailersResponse{},
		}
	}
	return resp
}
