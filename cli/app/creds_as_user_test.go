package app

import (
	"context"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/cli/internal/fakeconnect"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svclogin "github.com/seagalia/flock/sdk/flock/services/login/v1"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	"github.com/seagalia/flock/sdk/flock/services/lookup/v1/svclookupconnect"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
	"golang.org/x/oauth2"
	"google.golang.org/protobuf/proto"
)

var _ = Describe("AsUserInterceptor", func() {
	It("sets as_user on unary and streaming calls", func() {
		fake := fakeconnect.New().
			Handle(svcvsconnect.VirtualServerReadVSProcedure, func(proto.Message) (proto.Message, error) {
				return &svcvs.VSResponse{}, nil
			}).
			HandleStream(svclookupconnect.LookupGetLogsStreamProcedure, func(proto.Message) ([]proto.Message, error) {
				return nil, nil
			})
		client := fake.Client(AsUserInterceptor("bob", []string{"g"}))

		_, err := svcvsconnect.NewVirtualServerClient(client).ReadVS(context.Background(), &svccore.UuidRequest{})
		Expect(err).NotTo(HaveOccurred())
		stream, err := svclookupconnect.NewLookupClient(client).GetLogsStream(context.Background(), &svclookup.GetLogsRequest{})
		Expect(err).NotTo(HaveOccurred())
		_, _ = stream.Receive()
		Expect(stream.Close()).To(Succeed())

		Expect(fake.RequestHeaders(svcvsconnect.VirtualServerReadVSProcedure)[0].Get(AsUserMetaKey)).
			To(MatchJSON(`{"username":"bob","groups":["g"]}`))
		Expect(fake.RequestHeaders(svclookupconnect.LookupGetLogsStreamProcedure)[0].Get(AsUserMetaKey)).
			To(MatchJSON(`{"username":"bob","groups":["g"]}`))
	})

	It("keeps the headers set by the token interceptor", func() {
		fake := fakeconnect.New().
			Handle(svcvsconnect.VirtualServerReadVSProcedure, func(proto.Message) (proto.Message, error) {
				return &svcvs.VSResponse{}, nil
			})
		creds := svclogin.TokenInterceptor(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "token"}))
		client := fake.Client(creds, AsUserInterceptor("alice", []string{"admins", "devs"}))

		_, err := svcvsconnect.NewVirtualServerClient(client).ReadVS(context.Background(), &svccore.UuidRequest{})

		Expect(err).NotTo(HaveOccurred())
		headers := fake.RequestHeaders(svcvsconnect.VirtualServerReadVSProcedure)[0]
		Expect(headers.Get("authorization")).To(Equal("Bearer token"))
		Expect(headers.Get(AsUserMetaKey)).To(MatchJSON(`{"username":"alice","groups":["admins","devs"]}`))
	})

	It("fails the call when the credentials can't be read", func() {
		fake := fakeconnect.New()
		creds := svclogin.TokenInterceptor(failingTokenSource{})
		client := fake.Client(creds, AsUserInterceptor("alice", nil))

		_, err := svcvsconnect.NewVirtualServerClient(client).ReadVS(context.Background(), &svccore.UuidRequest{})

		Expect(err).To(MatchError(ContainSubstring("expired")))
		Expect(fake.Calls()).To(BeEmpty())
	})
})

type failingTokenSource struct{}

func (failingTokenSource) Token() (*oauth2.Token, error) {
	return nil, connect.NewError(connect.CodeUnauthenticated, "expired")
}
