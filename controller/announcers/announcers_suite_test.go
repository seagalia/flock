package announcers_test

import (
	svcdiscovery "github.com/seagalia/flock/sdk/flock/services/discovery/v1"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/seagalia/flock/controller/announcers"
)

func TestAnnouncers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Announcers Suite")
}

func EventuallyAtomic(actual *int64) AsyncAssertion {
	return Eventually(func() int {
		return int(atomic.LoadInt64(actual))
	})
}

func ConsistentlyAtomic(actual *int64) AsyncAssertion {
	return Consistently(func() int {
		return int(atomic.LoadInt64(actual))
	}, 300*time.Millisecond)
}

type AnnouncerListenerTest struct {
	onAnnounce func(evName EventName, res []*svcdiscovery.Resource)
}

func (a *AnnouncerListenerTest) SetOnAnnounce(onAnnounce func(evName EventName, res []*svcdiscovery.Resource)) {
	a.onAnnounce = onAnnounce
}

func (a *AnnouncerListenerTest) OnAnnounce(evName EventName, res []*svcdiscovery.Resource) {
	if a.onAnnounce == nil {
		return
	}
	a.onAnnounce(evName, res)
}
