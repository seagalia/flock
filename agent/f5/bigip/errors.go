package bigip

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// Error is a call to a BIG-IP that failed, on the device or before reaching it.
type Error struct {
	// Status is the HTTP status, or the code of the failed AS3 result; 0 when the device was not reached.
	Status int
	// Code is the F5 error code found in the message, like 01070356 for a feature not licensed.
	Code    string
	Message string
	// timeout tells an AS3 task was still running when the request timeout passed
	timeout bool
}

func (e *Error) Error() string {
	if e.Status == 0 {
		return "bigip: " + e.Message
	}
	return fmt.Sprintf("bigip: %d %s", e.Status, e.Message)
}

// Retryable tells the same call may succeed later: the device was unreachable, busy with another declaration,
// failing on its side, or the credentials were refused (a password being rotated). A 500 carrying an F5 error code
// is the device refusing the configuration, retrying it gives the same answer.
func (e *Error) Retryable() bool {
	switch {
	case e.Status == 0 || e.timeout:
		return true
	case e.Status == http.StatusUnauthorized || e.Status == http.StatusServiceUnavailable:
		return true
	case e.Status == http.StatusInternalServerError && e.Code != "":
		return false
	default:
		return e.Status >= 500
	}
}

const notLicensedCode = "01070356"

func IsNotLicensed(err error) bool {
	var bigipErr *Error
	return errors.As(err, &bigipErr) && bigipErr.Code == notLicensedCode
}

// f5Code matches the F5 error codes at the start of a message, "01070356:3: Load balancing feature not licensed."
var f5Code = regexp.MustCompile(`\b([0-9a-f]{8}):\d+:`)

func codeOf(message string) string {
	match := f5Code.FindStringSubmatch(message)
	if match == nil {
		return ""
	}
	return match[1]
}

type as3Result struct {
	Code     int    `json:"code"`
	Message  string `json:"message"`
	Response string `json:"response"`
	Tenant   string `json:"tenant"`
}

// parseError reads the error answered by iControl REST ({"code","message"}) or AS3 (with "errors" when the
// declaration is invalid, with "results" when a tenant failed).
func parseError(status int, body []byte) *Error {
	var answer struct {
		Message string      `json:"message"`
		Errors  []string    `json:"errors"`
		Results []as3Result `json:"results"`
	}
	if json.Unmarshal(body, &answer) != nil || (answer.Message == "" && len(answer.Results) == 0) {
		return &Error{Status: status, Message: strings.TrimSpace(http.StatusText(status) + " " + truncate(string(body)))}
	}
	for _, result := range answer.Results {
		if result.Code >= 400 {
			return resultError(result)
		}
	}
	message := answer.Message
	if len(answer.Errors) > 0 {
		message += ": " + strings.Join(answer.Errors, "; ")
	}
	return &Error{Status: status, Code: codeOf(message), Message: message}
}

func resultError(result as3Result) *Error {
	message := result.Message
	if result.Response != "" {
		message += ": " + result.Response
	}
	return &Error{Status: result.Code, Code: codeOf(result.Response), Message: message}
}

func truncate(s string) string {
	const limit = 512
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}
