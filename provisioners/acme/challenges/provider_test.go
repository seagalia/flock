package challenges_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/seagalia/flock/provisioners/acme/certmanagers/mocks"
	"github.com/seagalia/flock/provisioners/acme/challenges"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	svcsecret "github.com/seagalia/flock/sdk/flock/services/secret/v1"
)

var _ = Describe("Provider", func() {
	var (
		table  *challenges.Table
		client *mocks.MockSecretClient

		requestsMu sync.Mutex
		requests   []*svcsecret.UpdateSecretRequest
		echoing    atomic.Bool
	)

	baseOf := func(uuid string) *secret.Secret {
		return &secret.Secret{
			UUID:     uuid,
			Name:     "name-" + uuid,
			Metadata: map[string]string{challenges.MetadataKey: "true"},
			Data:     map[string]string{"ignored": "x"},
		}
	}

	recorded := func() []*svcsecret.UpdateSecretRequest {
		requestsMu.Lock()
		defer requestsMu.Unlock()
		return append([]*svcsecret.UpdateSecretRequest(nil), requests...)
	}

	// answer records each write and, while echoing is set, shows it to the table like ADS would. A write on an ended
	// ctx fails like with the real client.
	answer := func(echo bool) {
		echoing.Store(echo)
		client.EXPECT().UpdateSecret(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, req *svcsecret.UpdateSecretRequest) (*svcsecret.SecretResponse, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				requestsMu.Lock()
				requests = append(requests, req)
				requestsMu.Unlock()
				if echoing.Load() {
					go table.Replace(req.GetUUID(), challenges.DecodeData(req.GetSecret().GetData()))
				}
				return &svcsecret.SecretResponse{}, nil
			}).AnyTimes()
	}

	BeforeEach(func() {
		table = challenges.NewTable()
		client = mocks.NewMockSecretClient(gomock.NewController(GinkgoT()))
		requests = nil
	})

	It("writes the challenge and waits for its echo", func(ctx SpecContext) {
		answer(true)
		base := baseOf("s1")
		provider := challenges.NewProvider(client, table, base, challenges.DefaultEchoTimeout)

		Expect(provider.Present(ctx, "a.test", "tok-1", "ka-1")).To(Succeed())

		Expect(recorded()).To(HaveLen(1))
		req := recorded()[0]
		Expect(req.GetUUID()).To(Equal("s1"))
		Expect(req.GetSecret().GetName()).To(Equal(base.Name))
		Expect(req.GetSecret().GetMetadata()).To(Equal(base.Metadata))
		Expect(req.GetSecret().GetData()[challenges.MarkerKey]).NotTo(BeEmpty())
		keyAuth, found := table.KeyAuthByToken("tok-1")
		Expect(found).To(BeTrue())
		Expect(keyAuth).To(Equal("ka-1"))
	})

	It("never writes back a challenge cleaned up", func(ctx SpecContext) {
		answer(true)
		provider := challenges.NewProvider(client, table, baseOf("s1"), challenges.DefaultEchoTimeout)

		Expect(provider.Present(ctx, "a.test", "tok-a", "ka-a")).To(Succeed())
		Expect(provider.CleanUp(ctx, "a.test", "tok-a", "ka-a")).To(Succeed())
		Expect(provider.Present(ctx, "b.test", "tok-b", "ka-b")).To(Succeed())

		Expect(recorded()).To(HaveLen(3))
		Expect(challenges.DecodeData(recorded()[2].GetSecret().GetData())).To(Equal(
			[]challenges.Challenge{{Domain: "b.test", Token: "tok-b", KeyAuth: "ka-b"}}))
	})

	It("cleans up a challenge on a ctx lego already cancelled", func(ctx SpecContext) {
		answer(true)
		provider := challenges.NewProvider(client, table, baseOf("s1"), challenges.DefaultEchoTimeout)
		Expect(provider.Present(ctx, "a.test", "tok-a", "ka-a")).To(Succeed())
		cancelled, cancel := context.WithCancel(ctx)
		cancel()

		Expect(provider.CleanUp(cancelled, "a.test", "tok-a", "ka-a")).To(Succeed())

		Expect(recorded()).To(HaveLen(2))
		Expect(challenges.DecodeData(recorded()[1].GetSecret().GetData())).To(BeEmpty())
	})

	It("flushes every live challenge with one write, even on a cancelled ctx", func(ctx SpecContext) {
		answer(true)
		provider := challenges.NewProvider(client, table, baseOf("s1"), challenges.DefaultEchoTimeout)
		Expect(provider.Present(ctx, "a.test", "tok-a", "ka-a")).To(Succeed())
		Expect(provider.Present(ctx, "b.test", "tok-b", "ka-b")).To(Succeed())
		cancelled, cancel := context.WithCancel(ctx)
		cancel()

		Expect(provider.Flush(cancelled)).To(Succeed())

		Expect(recorded()).To(HaveLen(3))
		Expect(challenges.DecodeData(recorded()[2].GetSecret().GetData())).To(BeEmpty())

		Expect(provider.Flush(ctx)).To(Succeed())
		Expect(recorded()).To(HaveLen(3))
	})

	It("keeps both challenges of two orders apart", func(ctx SpecContext) {
		answer(true)
		first := challenges.NewProvider(client, table, baseOf("s1"), challenges.DefaultEchoTimeout)
		second := challenges.NewProvider(client, table, baseOf("s2"), challenges.DefaultEchoTimeout)

		var wg sync.WaitGroup
		errs := make([]error, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			errs[0] = first.Present(ctx, "a.test", "tok-1", "ka-1")
		}()
		go func() {
			defer wg.Done()
			errs[1] = second.Present(ctx, "b.test", "tok-2", "ka-2")
		}()
		wg.Wait()

		Expect(errs).To(Equal([]error{nil, nil}))
		requests := recorded()
		uuids := make([]string, 0, len(requests))
		for _, req := range requests {
			uuids = append(uuids, req.GetUUID())
		}
		Expect(uuids).To(ConsistOf("s1", "s2"))
		for _, req := range recorded() {
			want := map[string]string{"s1": "tok-1", "s2": "tok-2"}[req.GetUUID()]
			decoded := challenges.DecodeData(req.GetSecret().GetData())
			Expect(decoded).To(HaveLen(1))
			Expect(decoded[0].Token).To(Equal(want))
		}
		_, found := table.KeyAuthByToken("tok-1")
		Expect(found).To(BeTrue())
		_, found = table.KeyAuthByToken("tok-2")
		Expect(found).To(BeTrue())
	})

	It("fails when the echo never comes", func(ctx SpecContext) {
		answer(false)
		provider := challenges.NewProvider(client, table, baseOf("s1"), 50*time.Millisecond)

		err := provider.Present(ctx, "a.test", "tok-1", "ka-1")

		Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
		Expect(err).To(MatchError(ContainSubstring("a.test")))

		echoing.Store(true)
		Expect(provider.Present(ctx, "b.test", "tok-2", "ka-2")).To(Succeed())
		last := recorded()[len(recorded())-1]
		Expect(challenges.DecodeData(last.GetSecret().GetData())).To(Equal(
			[]challenges.Challenge{{Domain: "b.test", Token: "tok-2", KeyAuth: "ka-2"}}))
	})

	It("refuses a 64th challenge before writing", func(ctx SpecContext) {
		answer(true)
		provider := challenges.NewProvider(client, table, baseOf("s1"), challenges.DefaultEchoTimeout)
		for i := range 63 {
			Expect(provider.Present(ctx, "a.test", fmt.Sprintf("tok-%d", i), "ka")).To(Succeed())
		}
		writes := len(recorded())

		Expect(provider.Present(ctx, "a.test", "tok-last", "ka")).NotTo(Succeed())
		Expect(recorded()).To(HaveLen(writes))
	})

	It("returns the error of the controller", func(ctx SpecContext) {
		client.EXPECT().UpdateSecret(gomock.Any(), gomock.Any()).Return(nil, errors.New("boom"))
		provider := challenges.NewProvider(client, table, baseOf("s1"), challenges.DefaultEchoTimeout)

		Expect(provider.Present(ctx, "a.test", "tok-1", "ka-1")).To(MatchError(ContainSubstring("boom")))

		answer(true)
		Expect(provider.Present(ctx, "b.test", "tok-2", "ka-2")).To(Succeed())
		Expect(challenges.DecodeData(recorded()[0].GetSecret().GetData())).To(Equal(
			[]challenges.Challenge{{Domain: "b.test", Token: "tok-2", KeyAuth: "ka-2"}}))
	})
})
