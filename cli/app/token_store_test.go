package app

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"time"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/seagalia/flock/cli/internal/fakeconnect"
	svclogin "github.com/seagalia/flock/sdk/flock/services/login/v1"
	"github.com/seagalia/flock/sdk/flock/services/login/v1/svcloginconnect"
)

var _ = Describe("sessionTokenSource", func() {
	var (
		fake   *fakeconnect.Conn
		path   string
		expiry time.Time
	)

	BeforeEach(func() {
		expiry = time.Now().Add(time.Hour).Truncate(time.Second)
		fake = fakeconnect.New().Handle(svcloginconnect.LoginGetTokenProcedure, func(proto.Message) (proto.Message, error) {
			return &svclogin.GetTokenResponse{Token: "fresh", ExpiresAt: timestamppb.New(expiry)}, nil
		})
		path = filepath.Join(GinkgoT().TempDir(), "session", "flock.token")
	})

	newSource := func(username, password string) *sessionTokenSource {
		loginClient := svcloginconnect.NewLoginClient(fake.Client())
		cnf := &Config{Username: username, Password: password}
		return &sessionTokenSource{ctx: context.Background(), path: path, cnf: cnf, loginClient: loginClient}
	}

	It("gives the stored token while it is valid", func() {
		Expect(writeStoredToken(path, &storedToken{Kind: "flock", AccessToken: "stored", Expiry: expiry})).To(Succeed())

		tok, err := newSource("alice", "secret").Token()

		Expect(err).NotTo(HaveOccurred())
		Expect(tok.AccessToken).To(Equal("stored"))
		Expect(fake.Calls()).To(BeEmpty())
	})

	It("logs in with the credentials and stores the flock token", func() {
		tok, err := newSource("alice", "secret").Token()

		Expect(err).NotTo(HaveOccurred())
		Expect(tok.AccessToken).To(Equal("fresh"))
		sent := fake.Requests(svcloginconnect.LoginGetTokenProcedure)[0].(*svclogin.GetTokenRequest)
		Expect(sent.GetUsername()).To(Equal("alice"))
		Expect(sent.GetPassword()).To(Equal("secret"))
		stored, err := readStoredToken(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.Kind).To(Equal("flock"))
		Expect(stored.AccessToken).To(Equal("fresh"))
		Expect(stored.Expiry).To(BeTemporally("==", expiry))
		info, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0600)))
	})

	It("logs in again with the credentials when the token expired", func() {
		expired := &storedToken{Kind: "flock", AccessToken: "old", Expiry: time.Now().Add(-time.Minute)}
		Expect(writeStoredToken(path, expired)).To(Succeed())

		tok, err := newSource("alice", "secret").Token()

		Expect(err).NotTo(HaveOccurred())
		Expect(tok.AccessToken).To(Equal("fresh"))
	})

	It("asks to log in again when the token expired and no password is given", func() {
		expired := &storedToken{Kind: "flock", AccessToken: "old", Expiry: time.Now().Add(-time.Minute)}
		Expect(writeStoredToken(path, expired)).To(Succeed())

		_, err := newSource("alice", "").Token()

		Expect(err).To(MatchError("your session expired, run flock login again"))
		Expect(fake.Calls()).To(BeEmpty())
	})

	It("asks to log in without a session", func() {
		_, err := newSource("", "").Token()

		Expect(err).To(MatchError("you are not logged in, run flock login"))
	})

	It("gives the error of the controller", func() {
		fake.Handle(svcloginconnect.LoginGetTokenProcedure, func(proto.Message) (proto.Message, error) {
			return nil, connect.NewError(connect.CodeUnauthenticated, "invalid credentials")
		})

		_, err := newSource("alice", "wrong").Token()

		Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnauthenticated))
		Expect(path).NotTo(BeAnExistingFile())
	})

	It("gives up on a controller that accepts the connection and never answers", func() {
		previous := loginCallTimeout
		loginCallTimeout = 100 * time.Millisecond
		DeferCleanup(func() { loginCallTimeout = previous })
		silent, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(silent.Close)
		go func() {
			// the connections are kept open and never read, like a controller stuck before the TLS handshake. Holding
			// them matters: a dropped connection is closed by the garbage collector, and the client sees a reset.
			var held []net.Conn
			for {
				conn, err := silent.Accept()
				if err != nil {
					for _, conn := range held {
						conn.Close() // nolint:errcheck
					}
					return
				}
				held = append(held, conn)
			}
		}()
		client, err := makeClient(&Config{Host: silent.Addr().String(), SkipVerify: true})
		Expect(err).NotTo(HaveOccurred())
		source := &sessionTokenSource{
			ctx:         context.Background(),
			path:        path,
			cnf:         &Config{Username: "alice", Password: "secret"},
			loginClient: svcloginconnect.NewLoginClient(client),
		}

		failed := make(chan error, 1)
		go func() {
			_, err := source.Token()
			failed <- err
		}()

		Eventually(failed, 5*time.Second).Should(Receive(&err))
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeDeadlineExceeded))
	})
})

var _ = Describe("writeStoredToken", func() {
	It("keeps the previous session readable when the move fails", func() {
		path := filepath.Join(GinkgoT().TempDir(), "session", "flock.token")
		expiry := time.Now().Add(time.Hour).Truncate(time.Second)
		Expect(writeStoredToken(path, &storedToken{Kind: "flock", AccessToken: "previous", Expiry: expiry})).To(Succeed())
		previousRename := renameFile
		renameFile = func(string, string) error { return errors.New("rename failed") }
		DeferCleanup(func() { renameFile = previousRename })

		err := writeStoredToken(path, &storedToken{Kind: "flock", AccessToken: "next", Expiry: expiry})

		Expect(err).To(HaveOccurred())
		kept, err := readStoredToken(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(kept.AccessToken).To(Equal("previous"))
		leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".session-*"))
		Expect(err).NotTo(HaveOccurred())
		Expect(leftovers).To(BeEmpty())
	})
})
