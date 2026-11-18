package app

import (
	"context"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/agent/f5/reconciler"
	"github.com/seagalia/flock/sdk/agentsdk/runtime"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/logging"
)

type recordingAdsHandler struct {
	typeUrl  string
	onSynced func()
}

func (r *recordingAdsHandler) TypeUrl() string {
	return r.typeUrl
}

func (r *recordingAdsHandler) Handle(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (r *recordingAdsHandler) Rerun(context.Context, string) error {
	return nil
}

func (r *recordingAdsHandler) SetCleanWhenDone(bool) {}

func (r *recordingAdsHandler) SetOnSynced(onSynced func()) {
	r.onSynced = onSynced
}

var _ = Describe("StartHandlers", func() {
	It("does not wait for the secrets before the sweep, the controller sends none when no resource points at one", func() {
		ctx, cancel := context.WithCancel(context.Background())
		resources := &recordingAdsHandler{typeUrl: helpers.TypeUrl[*vip.VIP]()}
		secrets := &recordingAdsHandler{typeUrl: helpers.TypeUrl[*secret.Secret]()}
		rt := runtime.New()
		rt.RegisterHandler(resources)
		rt.RegisterHandler(secrets)
		a := &App{
			ctx:   ctx,
			entry: logging.Component("app"),
			rec:   reconciler.New(reconciler.Options{}, nil, nil, nil, stores.NewStateStore(), nil),
			rt:    rt,
		}
		wg := &sync.WaitGroup{}

		a.StartHandlers(wg, make(chan struct{}, 2))

		Expect(resources.onSynced).ToNot(BeNil())
		Expect(secrets.onSynced).To(BeNil())
		cancel()
		wg.Wait()
	})
})
