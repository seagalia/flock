package svclogin

import (
	"context"

	"connectrpc.com/connect/v2"
	"golang.org/x/oauth2"
)

// TokenInterceptor sends the access token of ts as a bearer token on every call of a connect client. A client whose
// bearer is an ID token puts it in AccessToken.
func TokenInterceptor(ts oauth2.TokenSource) connect.ClientInterceptor {
	return func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			tok, err := ts.Token()
			if err != nil {
				return nil, err
			}
			// connect.Client attaches a CallInfo to ctx before running its interceptors
			callInfo, _ := connect.CallInfoForClientContext(ctx)
			callInfo.RequestHeader().Set("authorization", "Bearer "+tok.AccessToken)
			return next(ctx, spec)
		}
	}
}
