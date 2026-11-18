package envoyctl

import (
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	streamv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/access_loggers/stream/v3"
	"google.golang.org/protobuf/types/known/structpb"
)

var accessLogStdout *streamv3.StdoutAccessLog

func init() {
	accessLogStdout = &streamv3.StdoutAccessLog{}
}

func SetAccessLogStdoutInJson(inJson bool) {
	if !inJson {
		accessLogStdout.AccessLogFormat = nil
		return
	}

	jsonFormat, _ := structpb.NewStruct(map[string]any{
		"start_time":            "%START_TIME%",
		"method":                "%REQ(:METHOD)%",
		"path":                  "%REQ(X-ENVOY-ORIGINAL-PATH?:PATH)%",
		"protocol":              "%PROTOCOL%",
		"response_code":         "%RESPONSE_CODE%",
		"response_flags":        "%RESPONSE_FLAGS%",
		"bytes_received":        "%BYTES_RECEIVED%",
		"bytes_sent":            "%BYTES_SENT%",
		"duration":              "%DURATION%",
		"upstream_service_time": "%RESP(X-ENVOY-UPSTREAM-SERVICE-TIME)%",
		"forwarded_for":         "%REQ(X-FORWARDED-FOR)%",
		"user_agent":            "%REQ(USER-AGENT)%",
		"request_id":            "%REQ(X-REQUEST-ID)%",
		"authority":             "%REQ(:AUTHORITY)%",
		"upstream_host":         "%UPSTREAM_HOST%",
	}) // nolint

	accessLogStdout.AccessLogFormat = &streamv3.StdoutAccessLog_LogFormat{
		LogFormat: &corev3.SubstitutionFormatString{
			Format: &corev3.SubstitutionFormatString_JsonFormat{
				JsonFormat: jsonFormat,
			},
		},
	}
}
