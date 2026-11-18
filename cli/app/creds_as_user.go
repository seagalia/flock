package app

import (
	"context"
	"encoding/json"

	"connectrpc.com/connect/v2"
)

const (
	AsUserMetaKey = "as_user"
)

// AsUserInterceptor makes every call act as username with groups, the controller allows it to admins only.
func AsUserInterceptor(username string, groups []string) connect.ClientInterceptor {
	return func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			asUser, err := AsUserHeader(username, groups)
			if err != nil {
				return nil, err
			}
			// connect.Client attaches a CallInfo to ctx before running its interceptors
			callInfo, _ := connect.CallInfoForClientContext(ctx)
			callInfo.RequestHeader().Set(AsUserMetaKey, asUser)
			return next(ctx, spec)
		}
	}
}

// AsUserHeader is the value of the as_user header acting as username with groups.
func AsUserHeader(username string, groups []string) (string, error) {
	asUser, err := json.Marshal(struct {
		Username string   `json:"username"`
		Groups   []string `json:"groups"`
	}{
		Username: username,
		Groups:   groups,
	})
	if err != nil {
		return "", err
	}
	return string(asUser), nil
}
