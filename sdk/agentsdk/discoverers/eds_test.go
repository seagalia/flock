package discoverers_test

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"

	"connectrpc.com/connect/v2"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"
	. "github.com/seagalia/flock/sdk/agentsdk/discoverers"
	"github.com/seagalia/flock/sdk/agentsdk/testhelpers"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/vigie/sdk/clients"
	eds "github.com/seagalia/vigie/sdk/vigie/eds/v1"
	"github.com/seagalia/vigie/sdk/vigie/eds/v1/edsconnect"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/encoding/protojson"
)

var _ = Describe("Eds", func() {
	var (
		mockCtrl *gomock.Controller
	)
	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
	})
	AfterEach(func() {
		mockCtrl.Finish()
	})
	Context("Run", func() {
		When("Use an http/https server ", func() {
			var server *ghttp.Server
			var urlServer *core.URL
			BeforeEach(func() {
				var err error
				server = ghttp.NewServer()
				urlServer, err = core.UrlToCoreUrl(server.URL())
				Expect(err).ToNot(HaveOccurred())
			})

			AfterEach(func() {
				server.Close()
			})
			It("should return targets", func() {
				targets := []*core.IPAddr{
					{
						Ip:   "127.0.0.1",
						Port: uint32(8080),
					},
					{
						Ip:   "127.0.0.1",
						Port: uint32(8081),
					},
				}
				server.AppendHandlers(
					func(w http.ResponseWriter, req *http.Request) {
						b, err := helpers.MarshalListProtoMessage[*core.IPAddr](
							protojson.MarshalOptions{},
							targets,
						)
						Expect(err).ToNot(HaveOccurred())
						_, err = w.Write(b)
						Expect(err).ToNot(HaveOccurred())
					},
				)

				sd, err := NewEds(urlServer, "", "", nil)
				Expect(err).ToNot(HaveOccurred())

				found := runDiscovery(sd)
				ipAddrs := <-found
				Expect(ipAddrs).To(HaveLen(2))
				Expect(ipAddrs[0].Ip).To(Equal("127.0.0.1"))
				Expect(ipAddrs[0].Port).To(Equal(uint32(8080)))
				Expect(ipAddrs[1].Ip).To(Equal("127.0.0.1"))
				Expect(ipAddrs[1].Port).To(Equal(uint32(8081)))
			})
			It("sends the user with the password an agent resolved as basic auth, and the headers", func() {
				server.AppendHandlers(ghttp.CombineHandlers(
					ghttp.VerifyBasicAuth("u", "v"),
					ghttp.VerifyHeaderKV("x-token", "v"),
					ghttp.RespondWith(http.StatusOK, "[]"),
				))
				urlServer.UserInfo = &core.URL_UserInfo{User: "u", PasswordSource: &core.URL_UserInfo_Password{Password: "v"}}

				sd, err := NewEds(urlServer, "", "", map[string]string{"x-token": "v"})
				Expect(err).ToNot(HaveOccurred())
				found := make(chan []*core.IPAddr, 1)
				Expect(sd.Run(context.Background(), found)).To(Succeed())
				Expect(server.ReceivedRequests()).To(HaveLen(1))
			})
			It("fails the run on an endpoint that isn't an ip address", func() {
				server.AppendHandlers(ghttp.RespondWith(http.StatusOK, `[{"ip": "web.local", "port": 80}]`))

				sd, err := NewEds(urlServer, "", "", nil)
				Expect(err).ToNot(HaveOccurred())

				found := make(chan []*core.IPAddr, 1)
				err = sd.Run(context.Background(), found)
				Expect(err).To(MatchError(ContainSubstring("web.local")))
				Expect(found).ToNot(Receive())
			})
			When("use service_name", func() {
				It("should return targets for this service name", func() {
					targets := []*core.IPAddr{
						{
							Ip:   "127.0.0.1",
							Port: uint32(8080),
						},
						{
							Ip:   "127.0.0.1",
							Port: uint32(8081),
						},
					}
					server.AppendHandlers(
						func(w http.ResponseWriter, req *http.Request) {
							err := req.ParseForm()
							Expect(err).ToNot(HaveOccurred())
							if req.Form.Get("service_name") != "test" {
								_, err := w.Write([]byte("[]"))
								Expect(err).ToNot(HaveOccurred())
								return
							}

							b, err := helpers.MarshalListProtoMessage[*core.IPAddr](
								protojson.MarshalOptions{},
								targets,
							)
							Expect(err).ToNot(HaveOccurred())
							_, err = w.Write(b)
							Expect(err).ToNot(HaveOccurred())
						},
					)

					sd, err := NewEds(urlServer, "test", "", nil)
					Expect(err).ToNot(HaveOccurred())

					found := runDiscovery(sd)
					ipAddrs := <-found
					Expect(ipAddrs).To(HaveLen(2))
					Expect(ipAddrs[0].Ip).To(Equal("127.0.0.1"))
					Expect(ipAddrs[0].Port).To(Equal(uint32(8080)))
					Expect(ipAddrs[1].Ip).To(Equal("127.0.0.1"))
					Expect(ipAddrs[1].Port).To(Equal(uint32(8081)))
				})
				It("should not return targets when service name is not found", func() {
					targets := []*core.IPAddr{
						{
							Ip:   "127.0.0.1",
							Port: uint32(8080),
						},
						{
							Ip:   "127.0.0.1",
							Port: uint32(8081),
						},
					}
					server.AppendHandlers(
						func(w http.ResponseWriter, req *http.Request) {
							err := req.ParseForm()
							Expect(err).ToNot(HaveOccurred())
							if req.Form.Get("service_name") != "test" {
								_, err := w.Write([]byte("[]"))
								Expect(err).ToNot(HaveOccurred())
								return
							}

							b, err := helpers.MarshalListProtoMessage[*core.IPAddr](
								protojson.MarshalOptions{},
								targets,
							)
							Expect(err).ToNot(HaveOccurred())
							_, err = w.Write(b)
							Expect(err).ToNot(HaveOccurred())
						},
					)

					sd, err := NewEds(urlServer, "not found", "", nil)
					Expect(err).ToNot(HaveOccurred())

					found := runDiscovery(sd)

					ipAddrs := <-found
					Expect(ipAddrs).To(BeEmpty())
				})
			})
		})
		When("Use a grpc server", func() {
			var client *testhelpers.MockEndpointDiscoveryServiceClient
			var baseURL string
			var opts clients.Options
			BeforeEach(func() {
				client = testhelpers.NewMockEndpointDiscoveryServiceClient(mockCtrl)
				SetGrpcEndpointDiscoClientFactory(func(u string, o clients.Options) (edsconnect.EndpointDiscoveryServiceClient, error) {
					baseURL = u
					opts = o
					return client, nil
				})
			})

			answer := func(endpoints ...*eds.Endpoint) {
				client.EXPECT().GetEndpoints(gomock.Any(), gomock.Any()).
					Return(&eds.GetEndpointsResponse{Endpoints: endpoints}, nil).Times(1)
			}

			// run calls Run once, with room on found for the one answer it may send
			run := func(u *core.URL, headers map[string]string) ([]*core.IPAddr, error) {
				sd, err := NewEds(u, "web", "", headers)
				Expect(err).ToNot(HaveOccurred())
				found := make(chan []*core.IPAddr, 1)
				err = sd.Run(context.Background(), found)
				if err != nil {
					Expect(found).ToNot(Receive())
					return nil, err
				}
				var ipAddrs []*core.IPAddr
				Expect(found).To(Receive(&ipAddrs))
				return ipAddrs, nil
			}

			It("maps grpc to an h2c base url, keeping the path", func() {
				_, err := NewEds(&core.URL{Scheme: "grpc", Host: "vigie", Port: 8080, Path: "/prefix"}, "web", "", nil)
				Expect(err).ToNot(HaveOccurred())

				Expect(baseURL).To(Equal("http://vigie:8080/prefix"))
				Expect(opts.TLSConfig).To(BeNil())
			})

			It("maps grpcs to a tls base url with the ca", func() {
				tlsServer := httptest.NewTLSServer(http.NotFoundHandler())
				defer tlsServer.Close()
				caRaw := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tlsServer.Certificate().Raw})
				expectedPool := x509.NewCertPool()
				expectedPool.AddCert(tlsServer.Certificate())

				_, err := NewEds(&core.URL{Scheme: "grpcs", Host: "vigie", Port: 8443}, "web", string(caRaw), nil)
				Expect(err).ToNot(HaveOccurred())

				Expect(baseURL).To(Equal("https://vigie:8443"))
				Expect(opts.TLSConfig).ToNot(BeNil())
				Expect(opts.TLSConfig.RootCAs.Equal(expectedPool)).To(BeTrue())
			})

			It("sends user_info as basic auth and the headers", func() {
				client.EXPECT().GetEndpoints(gomock.Any(), gomock.Any()).DoAndReturn(
					func(ctx context.Context, req *eds.GetEndpointsRequest) (*eds.GetEndpointsResponse, error) {
						callInfo, ok := connect.CallInfoForClientContext(ctx)
						Expect(ok).To(BeTrue())
						Expect(callInfo.RequestHeader().Get("x-token")).To(Equal("secret"))
						Expect(req.GetServiceName()).To(Equal("web"))
						return &eds.GetEndpointsResponse{}, nil
					}).Times(1)

				u := &core.URL{Scheme: "grpc", Host: "vigie", Port: 8080, UserInfo: &core.URL_UserInfo{User: "flock", PasswordSource: &core.URL_UserInfo_Password{Password: "secret"}}}
				_, err := run(u, map[string]string{"x-token": "secret"})
				Expect(err).ToNot(HaveOccurred())

				Expect(baseURL).To(Equal("http://vigie:8080"))
				Expect(opts.Username).To(Equal("flock"))
				Expect(opts.Password).To(Equal("secret"))
			})

			It("converts the endpoints", func() {
				answer(&eds.Endpoint{Ip: "127.0.0.1", Port: 8080, LoadBalancingWeight: 3})

				ipAddrs, err := run(&core.URL{Scheme: "grpc", Host: "vigie", Port: 8080}, nil)
				Expect(err).ToNot(HaveOccurred())

				Expect(ipAddrs).To(HaveLen(1))
				Expect(ipAddrs[0].GetIp()).To(Equal("127.0.0.1"))
				Expect(ipAddrs[0].GetPort()).To(Equal(uint32(8080)))
				Expect(ipAddrs[0].GetLoadBalancingWeight()).To(Equal(uint32(3)))
			})

			It("fails the run on an endpoint that isn't an ip address", func() {
				answer(&eds.Endpoint{Ip: "web.local", Port: 80})

				_, err := run(&core.URL{Scheme: "grpc", Host: "vigie", Port: 8080}, nil)
				Expect(err).To(MatchError(ContainSubstring("web.local")))
			})

			It("refuses an unknown scheme", func() {
				_, err := NewEds(&core.URL{Scheme: "ftp", Host: "vigie", Port: 21}, "web", "", nil)
				Expect(err).To(MatchError(ContainSubstring(`"ftp"`)))
			})
		})
	})
})
