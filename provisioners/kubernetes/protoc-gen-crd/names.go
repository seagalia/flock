package main

import (
	"strings"
	"unicode"
)

// acronyms keep one case in a name, as the Kubernetes API conventions ask: all lowercase at the start of a JSON name,
// all uppercase anywhere else.
var acronyms = map[string]bool{
	"api": true, "bgp": true, "ca": true, "cpu": true, "dns": true, "eds": true, "grpc": true, "hpack": true,
	"http": true, "http1": true, "http2": true, "http3": true, "id": true, "ip": true, "json": true, "lb": true,
	"quic": true, "san": true, "sni": true, "tcp": true, "tls": true, "ttl": true, "udp": true, "uri": true,
	"url": true, "uuid": true, "vip": true, "waf": true,
}

// httpMethods are proper names, written uppercase as enum values (`GET`), while `options` stays a plain word in
// field names.
var httpMethods = map[string]bool{
	"get": true, "head": true, "post": true, "put": true, "delete": true, "connect": true, "options": true,
	"trace": true, "patch": true,
}

func goWord(word string) string {
	lower := strings.ToLower(word)
	if acronyms[lower] {
		return strings.ToUpper(lower)
	}
	runes := []rune(lower)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// splitPascal splits a proto message or enum name into words, an uppercase run being one word (`IPAddrGroup` gives
// `IP`, `Addr`, `Group`) and digits staying with the word before them (`Http2Protocol` gives `Http2`, `Protocol`).
func splitPascal(name string) []string {
	runes := []rune(name)
	var words []string
	start := 0
	for i := 1; i < len(runes); i++ {
		previous, current := runes[i-1], runes[i]
		nextIsLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
		endsLowerWord := unicode.IsLower(previous) || unicode.IsDigit(previous)
		endsUpperRun := unicode.IsUpper(previous) && nextIsLower
		if unicode.IsUpper(current) && (endsLowerWord || endsUpperRun) {
			words = append(words, string(runes[start:i]))
			start = i
		}
	}
	return append(words, string(runes[start:]))
}

// typeName is the Go name of a proto message or enum name, `HttpHealthCheck` giving `HTTPHealthCheck`.
func typeName(protoName string) string {
	var name strings.Builder
	for _, word := range splitPascal(protoName) {
		name.WriteString(goWord(word))
	}
	return name.String()
}

// fieldNames gives the Go and JSON names of a snake_case proto field name, `enable_tls` giving `EnableTLS` and
// `enableTLS`.
func fieldNames(protoName string) (goName, jsonName string) {
	words := strings.Split(protoName, "_")
	// a few fields are named in PascalCase (`VirtualHost.PathPrefix`)
	if !strings.Contains(protoName, "_") && unicode.IsUpper([]rune(protoName)[0]) {
		words = splitPascal(protoName)
	}
	var goBuilder, jsonBuilder strings.Builder
	for i, word := range words {
		goBuilder.WriteString(goWord(word))
		if i == 0 {
			jsonBuilder.WriteString(strings.ToLower(word))
			continue
		}
		jsonBuilder.WriteString(goWord(word))
	}
	return goBuilder.String(), jsonBuilder.String()
}

// enumValue is the CamelCase value the CRD uses for a proto enum value (`ROUND_ROBIN` gives `RoundRobin`), with
// acronyms and HTTP methods uppercase.
func enumValue(protoName string) string {
	var value strings.Builder
	for word := range strings.SplitSeq(protoName, "_") {
		if httpMethods[strings.ToLower(word)] {
			value.WriteString(strings.ToUpper(word))
			continue
		}
		value.WriteString(goWord(word))
	}
	return value.String()
}
