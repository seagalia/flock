package app

import (
	"context"
	"errors"

	"connectrpc.com/grpchealth/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakePinger struct {
	err error
}

func (p fakePinger) Ping(context.Context) error {
	return p.err
}

var _ = Describe("healthChecker", func() {
	check := func(dependencies ...dependency) grpchealth.Status {
		resp, err := newHealthChecker(dependencies...).Check(context.Background(), &grpchealth.CheckRequest{})
		Expect(err).ToNot(HaveOccurred())
		return resp.Status
	}

	It("is serving while the database and nats answer", func() {
		status := check(dependency{name: "database", pinger: fakePinger{}}, dependency{name: "nats", pinger: fakePinger{}})
		Expect(status).To(Equal(grpchealth.StatusServing))
	})

	It("is not serving when the database does not answer", func() {
		status := check(dependency{name: "database", pinger: fakePinger{err: errors.New("sql: database is closed")}})
		Expect(status).To(Equal(grpchealth.StatusNotServing))
	})

	It("is not serving when nats does not answer", func() {
		status := check(
			dependency{name: "database", pinger: fakePinger{}},
			dependency{name: "nats", pinger: fakePinger{err: errors.New("nats: connection closed")}},
		)
		Expect(status).To(Equal(grpchealth.StatusNotServing))
	})

	It("gives each dependency a deadline, nats can't flush without one", func() {
		var hasDeadline bool
		check(dependency{name: "nats", pinger: deadlineSpy(func(ctx context.Context) {
			_, hasDeadline = ctx.Deadline()
		})})
		Expect(hasDeadline).To(BeTrue())
	})
})

type deadlineSpy func(ctx context.Context)

func (s deadlineSpy) Ping(ctx context.Context) error {
	s(ctx)
	return nil
}
