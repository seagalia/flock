package svclogin_test

import (
	"context"
	"errors"
	"maps"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/oauth2"

	. "github.com/seagalia/flock/sdk/flock/services/login/v1"
)

var _ = Describe("TokenInterceptor", func() {
	call := func(ts oauth2.TokenSource) (map[string][]string, bool, error) {
		ctx, callInfo := connect.NewClientContext(context.Background())
		called := false
		next := func(context.Context, connect.Spec) (connect.ClientStream, error) {
			called = true
			return nil, nil
		}
		_, err := TokenInterceptor(ts)(next)(ctx, connect.Spec{})
		return maps.Collect(callInfo.RequestHeader().All()), called, err
	}

	It("sends the token as the only auth header", func() {
		header, called, err := call(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "x"}))

		Expect(err).ToNot(HaveOccurred())
		Expect(called).To(BeTrue())
		Expect(header).To(HaveLen(1))
		Expect(header).To(HaveKeyWithValue("Authorization", []string{"Bearer x"}))
	})

	It("fails the call when the token source fails", func() {
		_, called, err := call(failingTokenSource{})

		Expect(err).To(MatchError(ContainSubstring("expired")))
		Expect(called).To(BeFalse())
	})
})

type failingTokenSource struct{}

func (failingTokenSource) Token() (*oauth2.Token, error) {
	return nil, errors.New("expired")
}
