package leases_test

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/agentsdk/leases"
	"github.com/seagalia/flock/sdk/agentsdk/testhelpers"
	svclease "github.com/seagalia/flock/sdk/flock/services/lease/v1"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	ttl          = 300 * time.Millisecond
	typeUrl      = "type.googleapis.com/flock.api.config.cert.v1.Certificate"
	resourceUuid = "11111111-1111-1111-1111-111111111111"
	instanceUuid = "22222222-2222-2222-2222-222222222222"
	leaseId      = "33333333-3333-3333-3333-333333333333"
)

func granted(expiresAt time.Time) *svclease.AcquireResponse {
	return &svclease.AcquireResponse{
		Granted:    true,
		LeaseId:    leaseId,
		HolderUuid: "me",
		HolderName: "me-agent",
		ExpiresAt:  timestamppb.New(expiresAt),
	}
}

func held(expiresAt time.Time) *svclease.AcquireResponse {
	return &svclease.AcquireResponse{
		HolderUuid: "other",
		HolderName: "other-agent",
		ExpiresAt:  timestamppb.New(expiresAt),
	}
}

var _ = Describe("Leases", func() {
	var (
		mockClient *testhelpers.MockLeaseClient
		client     *leases.Client
	)

	BeforeEach(func() {
		// the specs use a ttl far below the real lower bound so they don't wait seconds
		leases.SetMinTTL(ttl)
		mockClient = testhelpers.NewMockLeaseClient(gomock.NewController(GinkgoT()))
		client = leases.NewClient(mockClient, instanceUuid)
	})

	// acquireLease gives a lease whose renewals all answer renew, released at the end of the spec
	acquireLease := func(ctx context.Context, renew func() (*svclease.RenewResponse, error)) *leases.Lease {
		mockClient.EXPECT().Acquire(gomock.Any(), gomock.Any()).Return(granted(time.Now().Add(ttl)), nil)
		mockClient.EXPECT().Renew(gomock.Any(), gomock.Any()).DoAndReturn(
			func(context.Context, *svclease.RenewRequest) (*svclease.RenewResponse, error) {
				return renew()
			}).AnyTimes()
		mockClient.EXPECT().Release(gomock.Any(), gomock.Any()).Return(&emptypb.Empty{}, nil).AnyTimes()
		lease, err := client.Acquire(ctx, typeUrl, resourceUuid, leases.WithTTL(ttl))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(lease.Release(context.Background())).To(Succeed()) })
		return lease
	}

	renewed := func() (*svclease.RenewResponse, error) { //nolint:unparam // matches the func type it stands in for
		return &svclease.RenewResponse{ExpiresAt: timestamppb.New(time.Now().Add(ttl))}, nil
	}

	Describe("Acquire", func() {
		It("gives the lease when granted", func() {
			var sent *svclease.AcquireRequest
			mockClient.EXPECT().Acquire(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, req *svclease.AcquireRequest) (*svclease.AcquireResponse, error) {
					sent = req
					return granted(time.Now().Add(ttl)), nil
				})
			mockClient.EXPECT().Renew(gomock.Any(), gomock.Any()).DoAndReturn(
				func(context.Context, *svclease.RenewRequest) (*svclease.RenewResponse, error) {
					return renewed()
				}).AnyTimes()
			mockClient.EXPECT().Release(gomock.Any(), gomock.Any()).Return(&emptypb.Empty{}, nil)

			lease, err := client.Acquire(context.Background(), typeUrl, resourceUuid, leases.WithTTL(ttl))
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { Expect(lease.Release(context.Background())).To(Succeed()) })

			Expect(lease.Id()).To(Equal(leaseId))
			Expect(sent.GetTypeUrl()).To(Equal(typeUrl))
			Expect(sent.GetUUID()).To(Equal(resourceUuid))
			Expect(sent.GetInstanceUuid()).To(Equal(instanceUuid))
			Expect(sent.GetTtl().AsDuration()).To(Equal(ttl))
		})

		It("sends DefaultTTL when no ttl is given", func() {
			var sent *svclease.AcquireRequest
			mockClient.EXPECT().Acquire(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, req *svclease.AcquireRequest) (*svclease.AcquireResponse, error) {
					sent = req
					return held(time.Now().Add(time.Hour)), nil
				})

			_, err := client.Acquire(context.Background(), typeUrl, resourceUuid)
			Expect(err).To(HaveOccurred())
			Expect(sent.GetTtl().AsDuration()).To(Equal(leases.DefaultTTL))
		})

		It("answers HeldError without WithWait", func() {
			expiresAt := time.Now().Add(time.Hour).Truncate(time.Millisecond)
			mockClient.EXPECT().Acquire(gomock.Any(), gomock.Any()).Return(held(expiresAt), nil).Times(1)

			lease, err := client.Acquire(context.Background(), typeUrl, resourceUuid, leases.WithTTL(ttl))
			Expect(lease).To(BeNil())
			var heldErr *leases.HeldError
			Expect(errors.As(err, &heldErr)).To(BeTrue())
			Expect(heldErr.HolderUuid).To(Equal("other"))
			Expect(heldErr.HolderName).To(Equal("other-agent"))
			Expect(heldErr.ExpiresAt).To(BeTemporally("==", expiresAt))
			Expect(heldErr.Error()).To(ContainSubstring("other-agent"))
		})

		It("waits for the lease with WithWait", func() {
			var calls atomic.Int32
			mockClient.EXPECT().Acquire(gomock.Any(), gomock.Any()).DoAndReturn(
				func(context.Context, *svclease.AcquireRequest) (*svclease.AcquireResponse, error) {
					switch calls.Add(1) {
					case 1:
						return held(time.Now().Add(200 * time.Millisecond)), nil
					case 2:
						return held(time.Now().Add(50 * time.Millisecond)), nil
					default:
						return granted(time.Now().Add(ttl)), nil
					}
				}).Times(3)
			mockClient.EXPECT().Renew(gomock.Any(), gomock.Any()).DoAndReturn(
				func(context.Context, *svclease.RenewRequest) (*svclease.RenewResponse, error) {
					return renewed()
				}).AnyTimes()
			mockClient.EXPECT().Release(gomock.Any(), gomock.Any()).Return(&emptypb.Empty{}, nil)

			lease, err := client.Acquire(
				context.Background(), typeUrl, resourceUuid, leases.WithWait(), leases.WithTTL(ttl),
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(lease.Release(context.Background())).To(Succeed())
			Expect(calls.Load()).To(Equal(int32(3)))
		})

		It("retries a held lease at the latest every waitPollMax", func() {
			leases.SetWaitPollMax(100 * time.Millisecond)
			var calls atomic.Int32
			mockClient.EXPECT().Acquire(gomock.Any(), gomock.Any()).DoAndReturn(
				func(context.Context, *svclease.AcquireRequest) (*svclease.AcquireResponse, error) {
					if calls.Add(1) == 1 {
						return held(time.Now().Add(time.Hour)), nil
					}
					return granted(time.Now().Add(ttl)), nil
				}).Times(2)
			mockClient.EXPECT().Renew(gomock.Any(), gomock.Any()).DoAndReturn(
				func(context.Context, *svclease.RenewRequest) (*svclease.RenewResponse, error) {
					return renewed()
				}).AnyTimes()
			mockClient.EXPECT().Release(gomock.Any(), gomock.Any()).Return(&emptypb.Empty{}, nil)

			start := time.Now()
			lease, err := client.Acquire(
				context.Background(), typeUrl, resourceUuid, leases.WithWait(), leases.WithTTL(ttl),
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(time.Since(start)).To(BeNumerically("<", time.Second))
			Expect(lease.Release(context.Background())).To(Succeed())
		})

		It("stops waiting when ctx ends", func() {
			var calls atomic.Int32
			mockClient.EXPECT().Acquire(gomock.Any(), gomock.Any()).DoAndReturn(
				func(context.Context, *svclease.AcquireRequest) (*svclease.AcquireResponse, error) {
					calls.Add(1)
					return held(time.Now().Add(time.Hour)), nil
				}).AnyTimes()

			ctx, cancel := context.WithCancel(context.Background())
			time.AfterFunc(50*time.Millisecond, cancel)
			_, err := client.Acquire(ctx, typeUrl, resourceUuid, leases.WithWait(), leases.WithTTL(ttl))
			Expect(err).To(MatchError(context.Canceled))
			callsAtCancel := calls.Load()
			Consistently(calls.Load, 200*time.Millisecond).Should(Equal(callsAtCancel))
		})

		It("returns the error of a failed Acquire", func() {
			mockClient.EXPECT().Acquire(gomock.Any(), gomock.Any()).
				Return(nil, connect.Errorf(connect.CodeUnavailable, "down")).Times(1)

			_, err := client.Acquire(context.Background(), typeUrl, resourceUuid, leases.WithTTL(ttl))
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnavailable))
		})

		It("returns the error of a failed Acquire with WithWait", func() {
			mockClient.EXPECT().Acquire(gomock.Any(), gomock.Any()).
				Return(nil, connect.Errorf(connect.CodeUnavailable, "down")).Times(1)

			_, err := client.Acquire(
				context.Background(), typeUrl, resourceUuid, leases.WithWait(), leases.WithTTL(ttl),
			)
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnavailable))
		})
	})

	Describe("renewal", func() {
		It("renews every ttl/3 until Release", func() {
			var renews atomic.Int32
			mockClient.EXPECT().Acquire(gomock.Any(), gomock.Any()).Return(granted(time.Now().Add(ttl)), nil)
			mockClient.EXPECT().Renew(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, req *svclease.RenewRequest) (*svclease.RenewResponse, error) {
					defer GinkgoRecover()
					Expect(req.GetLeaseId()).To(Equal(leaseId))
					Expect(req.GetTtl().AsDuration()).To(Equal(ttl))
					renews.Add(1)
					return renewed()
				}).AnyTimes()
			var released *svclease.ReleaseRequest
			mockClient.EXPECT().Release(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, req *svclease.ReleaseRequest) (*emptypb.Empty, error) {
					released = req
					return &emptypb.Empty{}, nil
				})

			lease, err := client.Acquire(context.Background(), typeUrl, resourceUuid, leases.WithTTL(ttl))
			Expect(err).ToNot(HaveOccurred())
			Eventually(renews.Load, ttl+50*time.Millisecond).Should(BeNumerically(">=", 2))

			Expect(lease.Release(context.Background())).To(Succeed())
			Expect(released.GetLeaseId()).To(Equal(leaseId))
			renewsAtRelease := renews.Load()
			Consistently(renews.Load, ttl).Should(Equal(renewsAtRelease))
			Expect(lease.Context().Done()).To(BeClosed())
		})

		It("ends Context with ErrLost when a renewal answers NotFound", func() {
			lease := acquireLease(context.Background(), func() (*svclease.RenewResponse, error) {
				return nil, connect.Errorf(connect.CodeNotFound, "lease not found")
			})

			Eventually(lease.Context().Done(), ttl).Should(BeClosed())
			Expect(context.Cause(lease.Context())).To(MatchError(leases.ErrLost))
		})

		It("ends Context with ErrLost when renewals fail past the expiry", func() {
			lease := acquireLease(context.Background(), func() (*svclease.RenewResponse, error) {
				return nil, connect.Errorf(connect.CodeUnavailable, "down")
			})

			Consistently(lease.Context().Done(), ttl-50*time.Millisecond).ShouldNot(BeClosed())
			Eventually(lease.Context().Done(), ttl).Should(BeClosed())
			Expect(context.Cause(lease.Context())).To(MatchError(leases.ErrLost))
		})

		It("ends Context with ErrLost at the expiry when renewals hang", func() {
			grantedAt := time.Now()
			mockClient.EXPECT().Acquire(gomock.Any(), gomock.Any()).Return(granted(grantedAt.Add(ttl)), nil)
			mockClient.EXPECT().Renew(gomock.Any(), gomock.Any()).DoAndReturn(
				func(ctx context.Context, _ *svclease.RenewRequest) (*svclease.RenewResponse, error) {
					<-ctx.Done()
					return nil, ctx.Err()
				}).AnyTimes()
			lease, err := client.Acquire(context.Background(), typeUrl, resourceUuid, leases.WithTTL(ttl))
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { Expect(lease.Release(context.Background())).To(Succeed()) })

			Eventually(lease.Context().Done(), 2*ttl).Should(BeClosed())
			Expect(time.Since(grantedAt)).To(BeNumerically("<", ttl+60*time.Millisecond))
			Expect(context.Cause(lease.Context())).To(MatchError(leases.ErrLost))
		})

		It("keeps renewing after the ctx of Acquire ended", func() {
			var renews atomic.Int32
			ctx, cancel := context.WithCancel(context.Background())
			lease := acquireLease(ctx, func() (*svclease.RenewResponse, error) {
				renews.Add(1)
				return renewed()
			})

			cancel()
			Expect(lease.Context().Done()).To(BeClosed())
			Expect(context.Cause(lease.Context())).To(MatchError(context.Canceled))
			renewsAtCancel := renews.Load()
			Eventually(renews.Load, ttl).Should(BeNumerically(">=", renewsAtCancel+2))
		})
	})

	Describe("Renew", func() {
		It("answers ErrLost on NotFound", func() {
			lease := acquireLease(context.Background(), func() (*svclease.RenewResponse, error) {
				return nil, connect.Errorf(connect.CodeNotFound, "lease not found")
			})

			Expect(lease.Renew(context.Background())).To(MatchError(leases.ErrLost))
			Expect(context.Cause(lease.Context())).To(MatchError(leases.ErrLost))
		})

		It("pushes the known expiry forward on success", func() {
			later := time.Now().Add(time.Hour).Truncate(time.Millisecond)
			lease := acquireLease(context.Background(), func() (*svclease.RenewResponse, error) {
				return &svclease.RenewResponse{ExpiresAt: timestamppb.New(later)}, nil
			})

			Expect(lease.KnownExpiry()).To(BeTemporally("<", later))
			Expect(lease.Renew(context.Background())).To(Succeed())
			Expect(lease.KnownExpiry()).To(BeTemporally("==", later))
		})
	})

	Describe("Release", func() {
		It("releases a lost lease with no error", func() {
			lease := acquireLease(context.Background(), func() (*svclease.RenewResponse, error) {
				return nil, connect.Errorf(connect.CodeNotFound, "lease not found")
			})

			Eventually(lease.Context().Done(), ttl).Should(BeClosed())
			Expect(lease.Release(context.Background())).To(Succeed())
		})

		It("can be called twice", func() {
			lease := acquireLease(context.Background(), renewed)

			Expect(lease.Release(context.Background())).To(Succeed())
			Expect(lease.Release(context.Background())).To(Succeed())
		})
	})
})

var _ = Describe("ttl bounds", func() {
	var (
		mockClient *testhelpers.MockLeaseClient
		client     *leases.Client
		sent       *svclease.AcquireRequest
	)

	BeforeEach(func() {
		mockClient = testhelpers.NewMockLeaseClient(gomock.NewController(GinkgoT()))
		client = leases.NewClient(mockClient, instanceUuid)
		mockClient.EXPECT().Acquire(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, req *svclease.AcquireRequest) (*svclease.AcquireResponse, error) {
				sent = req
				return granted(time.Now().Add(req.GetTtl().AsDuration())), nil
			})
		mockClient.EXPECT().Release(gomock.Any(), gomock.Any()).Return(&emptypb.Empty{}, nil)
	})

	It("brings a ttl over the controller's upper bound down to it", func() {
		lease, err := client.Acquire(context.Background(), typeUrl, resourceUuid, leases.WithTTL(time.Hour))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(lease.Release(context.Background())).To(Succeed()) })

		Expect(sent.GetTtl().AsDuration()).To(Equal(5 * time.Minute))
		Expect(lease.RenewalPeriod()).To(Equal(100 * time.Second))
	})

	It("brings a zero ttl up to the controller's lower bound", func() {
		lease, err := client.Acquire(context.Background(), typeUrl, resourceUuid, leases.WithTTL(0))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(lease.Release(context.Background())).To(Succeed()) })

		Expect(sent.GetTtl().AsDuration()).To(Equal(10 * time.Second))
		Expect(lease.RenewalPeriod()).To(Equal(10 * time.Second / 3))
	})
})
