package contexes

import (
	"context"
	"fmt"

	"connectrpc.com/connect/v2"
)

const (
	UsernameMetaKey = "username"
	GroupsMetaKey   = "groups"
)

// retrieveHeader gives a request header of the call served with ctx
func retrieveHeader(ctx context.Context, key string) (string, error) {
	callInfo, ok := connect.CallInfoForServerContext(ctx)
	if !ok {
		return "", fmt.Errorf("Missing data")
	}
	value := callInfo.RequestHeader().Get(key)
	if value == "" {
		return "", fmt.Errorf("Missing data")
	}
	return value, nil
}

func RetrieveAuthorization(ctx context.Context) (string, error) {
	return retrieveHeader(ctx, "authorization")
}

// userInfoKey is unexported so that only this package can set the identity:
// anything a client sends in request headers can never be mistaken for it.
type userInfoKey struct{}

type userInfo struct {
	username string
	groups   []string
}

func WithUserInfo(ctx context.Context, username string, groups []string) context.Context {
	return context.WithValue(ctx, userInfoKey{}, userInfo{username: username, groups: groups})
}

func RetrieveUserInfo(ctx context.Context) (string, []string, bool) {
	info, ok := ctx.Value(userInfoKey{}).(userInfo)
	if !ok {
		return "", []string{}, false
	}
	return info.username, info.groups, true
}

// breakGlassKey is unexported: only the controller's verification of a break-glass token sets it through
// WithBreakGlass, so no header or username can claim it.
type breakGlassKey struct{}

func WithBreakGlass(ctx context.Context) context.Context {
	return context.WithValue(ctx, breakGlassKey{}, struct{}{})
}

func IsBreakGlass(ctx context.Context) bool {
	_, ok := ctx.Value(breakGlassKey{}).(struct{})
	return ok
}
