package common

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"connectrpc.com/connect/v2"
	msg "github.com/ArthurHlt/messages"
)

// codeNames are the grpc names the cli prints codes with, connect only has lowercase tokens ("not_found").
var codeNames = map[connect.Code]string{
	0:                              "OK",
	connect.CodeCanceled:           "Canceled",
	connect.CodeUnknown:            "Unknown",
	connect.CodeInvalidArgument:    "InvalidArgument",
	connect.CodeDeadlineExceeded:   "DeadlineExceeded",
	connect.CodeNotFound:           "NotFound",
	connect.CodeAlreadyExists:      "AlreadyExists",
	connect.CodePermissionDenied:   "PermissionDenied",
	connect.CodeResourceExhausted:  "ResourceExhausted",
	connect.CodeFailedPrecondition: "FailedPrecondition",
	connect.CodeAborted:            "Aborted",
	connect.CodeOutOfRange:         "OutOfRange",
	connect.CodeUnimplemented:      "Unimplemented",
	connect.CodeInternal:           "Internal",
	connect.CodeUnavailable:        "Unavailable",
	connect.CodeDataLoss:           "DataLoss",
	connect.CodeUnauthenticated:    "Unauthenticated",
}

func CodeName(code connect.Code) string {
	name, ok := codeNames[code]
	if !ok {
		return fmt.Sprintf("Code(%d)", code)
	}
	return name
}

func StringToStatusCode(str string) connect.Code {
	for code, name := range codeNames {
		if name == str {
			return code
		}
	}
	return connect.CodeUnknown
}

var regexRpc = regexp.MustCompile(`rpc error: code = (?P<code>\w+) desc = (?P<desc>.+)`)

// regexConnect matches a connect error message ("not_found: vs missing"), the flock agent writes
// resource status errors wrapping them (e.g. "failed to add: not_found: vs missing").
var regexConnect = regexp.MustCompile(`(?:^|: )(?P<code>canceled|unknown|invalid_argument|deadline_exceeded|not_found|already_exists|permission_denied|resource_exhausted|failed_precondition|aborted|out_of_range|unimplemented|internal|unavailable|data_loss|unauthenticated): (?P<desc>.+)`)
var regexCleanAgent = regexp.MustCompile(`(.+) UUID:`)

func WrapError(message string, err error) error {
	if connectErr, ok := errors.AsType[*connect.Error](err); ok {
		return connect.NewError(connectErr.Code(), fmt.Sprintf("%s: %s", message, connectErr.Message()))
	}
	return fmt.Errorf("%s: %w", message, err)
}

func RpcErrorFormat(err error) error {
	if connectErr, ok := errors.AsType[*connect.Error](err); ok {
		messg, _ := url.QueryUnescape(connectErr.Message()) // nolint:errcheck
		err = fmt.Errorf("%s (Code: %s)", msg.Blue(messg), msg.Yellow(CodeName(connectErr.Code())))
	}
	return err
}

func RpcErrorFromString(err string) error {
	code, desc, isRpcError := parseRpcError(err)
	isUnimplemented := strings.Contains(err, "code = Unimplemented") || (isRpcError && code == connect.CodeUnimplemented)
	if isUnimplemented {
		return connect.NewError(connect.CodeUnimplemented, "Unimplemented feature")
	}
	if !isRpcError {
		return errors.New(err)
	}
	if regexCleanAgent.MatchString(desc) {
		desc = regexCleanAgent.FindStringSubmatch(desc)[1]
	}
	return connect.NewError(code, desc)
}

// parseRpcError reads a grpc status error string or a connect error string.
func parseRpcError(err string) (connect.Code, string, bool) {
	if match := regexRpc.FindStringSubmatch(err); match != nil {
		return StringToStatusCode(match[1]), match[2], true
	}
	if match := regexConnect.FindStringSubmatch(err); match != nil {
		var code connect.Code
		if unmarshalErr := code.UnmarshalText([]byte(match[1])); unmarshalErr != nil {
			return 0, "", false
		}
		return code, match[2], true
	}
	return 0, "", false
}
