package certmanagers_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/go-acme/lego/v5/acme"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/provisioners/acme/challenges"
	"github.com/seagalia/flock/provisioners/acme/params"
	"github.com/seagalia/flock/sdk/agentsdk/testhelpers"
	svclease "github.com/seagalia/flock/sdk/flock/services/lease/v1"
	svcsecret "github.com/seagalia/flock/sdk/flock/services/secret/v1"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	agentUuid    = "agent-1"
	instanceUuid = "instance-1"
	leaseId      = "lease-1"
)

// leafPEM gives a self-signed certificate valid between notBefore and notAfter, with its PKCS #8 key.
func leafPEM(domain string, notBefore, notAfter time.Time) (certPEM, keyPEM string) {
	return leafPEMFor([]string{domain}, notBefore, notAfter)
}

func leafPEMFor(domains []string, notBefore, notAfter time.Time) (certPEM, keyPEM string) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).NotTo(HaveOccurred())
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: domains[0]},
		DNSNames:     domains,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	Expect(err).NotTo(HaveOccurred())
	keyDer, err := x509.MarshalPKCS8PrivateKey(key)
	Expect(err).NotTo(HaveOccurred())
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDer}))
	return certPEM, keyPEM
}

func freshLeafPEM(domain string) (certPEM, keyPEM string) { //nolint:unparam // the call sites show the value
	return leafPEM(domain, time.Now().Add(-time.Hour), time.Now().Add(90*24*time.Hour))
}

// account gives the account of a CA and its key, as the secret of a certificate holds them.
func account() (registration *params.RegistrationRequest, accountKey *params.KeyPemRequest) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).NotTo(HaveOccurred())
	registration = &params.RegistrationRequest{ExtendedAccount: &acme.ExtendedAccount{
		Status:   "valid",
		Location: "https://ca.example.com/acct/1",
	}}
	return registration, &params.KeyPemRequest{Signer: key}
}

// resolvedAccount gives the account values the ads handler puts in the metadata in place of their secret refs.
func resolvedAccount() (registrationJSON, accountKeyPEM string) {
	registration, accountKey := account()
	raw, err := json.Marshal(registration.ExtendedAccount)
	Expect(err).NotTo(HaveOccurred())
	accountKeyPEM, err = accountKey.ToValue()
	Expect(err).NotTo(HaveOccurred())
	return string(raw), accountKeyPEM
}

// goroutineReporter collects the failures of gomock from any goroutine. The manager calls its clients from its own
// goroutines, where a ginkgo failure panics out of the whole suite instead of failing the spec.
type goroutineReporter struct {
	mutex    sync.Mutex
	failures []string
}

func (r *goroutineReporter) Errorf(format string, args ...any) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

// Fatalf stops the calling goroutine like testing.T does, its defers still run
func (r *goroutineReporter) Fatalf(format string, args ...any) {
	r.Errorf(format, args...)
	runtime.Goexit()
}

func (r *goroutineReporter) Helper() {}

func (r *goroutineReporter) Failures() []string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return slices.Clone(r.failures)
}

// newMockController fails the spec at its end with what the mocks reported, missing calls included
func newMockController() *gomock.Controller {
	reporter := &goroutineReporter{}
	ctrl := gomock.NewController(reporter)
	DeferCleanup(func() {
		finished := make(chan struct{})
		// Finish ends with Fatalf on missing calls, which must not stop the goroutine of ginkgo
		go func() {
			defer close(finished)
			ctrl.Finish()
		}()
		<-finished
		Expect(reporter.Failures()).To(BeEmpty())
	})
	return ctrl
}

// fakeIdentity stands for the aggregator, which learns the uuid of the provisioner from the controller
type fakeIdentity struct {
	mutex sync.Mutex
	uuid  string
	known chan struct{}
}

func newFakeIdentity() *fakeIdentity {
	return &fakeIdentity{known: make(chan struct{})}
}

func (f *fakeIdentity) give(uuid string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.uuid = uuid
	close(f.known)
}

func (f *fakeIdentity) AgentUuid() string {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.uuid
}

func (f *fakeIdentity) WaitAgentUuid(ctx context.Context) (string, error) {
	select {
	case <-f.known:
		return f.AgentUuid(), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// grantingLease is a controller granting every lease and accepting its renewals and releases
func grantingLease(ctrl *gomock.Controller) *testhelpers.MockLeaseClient {
	leaseClient := testhelpers.NewMockLeaseClient(ctrl)
	leaseClient.EXPECT().Acquire(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *svclease.AcquireRequest) (*svclease.AcquireResponse, error) {
			return grantedLease(req), nil
		}).AnyTimes()
	leaseClient.EXPECT().Renew(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *svclease.RenewRequest) (*svclease.RenewResponse, error) {
			return renewedLease(req), nil
		}).AnyTimes()
	leaseClient.EXPECT().Release(gomock.Any(), gomock.Any()).Return(&emptypb.Empty{}, nil).AnyTimes()
	return leaseClient
}

func grantedLease(req *svclease.AcquireRequest) *svclease.AcquireResponse {
	return &svclease.AcquireResponse{
		Granted:    true,
		LeaseId:    leaseId,
		HolderUuid: agentUuid,
		ExpiresAt:  timestamppb.New(time.Now().Add(req.GetTtl().AsDuration())),
	}
}

func renewedLease(req *svclease.RenewRequest) *svclease.RenewResponse {
	return &svclease.RenewResponse{ExpiresAt: timestamppb.New(time.Now().Add(req.GetTtl().AsDuration()))}
}

func heldLease(expiresAt time.Time) *svclease.AcquireResponse {
	return &svclease.AcquireResponse{
		Granted:    false,
		HolderUuid: "agent-2",
		HolderName: "provisioner-2",
		ExpiresAt:  timestamppb.New(expiresAt),
	}
}

// isChallengeSecretList tells a listing of the challenge secrets of a certificate from the other listings of secrets
func isChallengeSecretList(req *svcsecret.ListSecretRequest) bool {
	for _, filter := range req.GetFilters() {
		if filter.GetKey() == challenges.MetadataKey {
			return true
		}
	}
	return false
}
