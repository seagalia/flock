package config_test

import (
	"time"

	"github.com/nats-io/nats.go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/seagalia/flock/controller/config"
	sdkconfig "github.com/seagalia/flock/sdk/config"
	"go.yaml.in/yaml/v2"
)

const minimalLogin = "login: {type: basic, basic_auth_options: [{username: u, password: p}]}\n" +
	"jwt_maker: {alg: HS256, private_key: secret}\n"

func readLogin(content string) *ServerLogin {
	login := &ServerLogin{}
	Expect(yaml.Unmarshal([]byte(content), login)).To(Succeed())
	return login
}

var _ = Describe("Server", func() {
	Context("Server", func() {
		It("should keep given listen address", func() {
			server := &Server{}
			err := yaml.Unmarshal([]byte("listen_addr: :9090\ntls: {cert_file: c, key_file: k}\n"+minimalLogin), server)
			Expect(err).ToNot(HaveOccurred())
			Expect(server.ListenAddr).To(Equal(":9090"))
		})
		It("should keep the dashboard disabled unless enabled", func() {
			server := &Server{}
			err := yaml.Unmarshal([]byte("tls: {cert_file: c, key_file: k}\n"+minimalLogin), server)
			Expect(err).ToNot(HaveOccurred())
			Expect(server.Dashboard.Enabled).To(BeFalse())

			err = yaml.Unmarshal([]byte("tls: {cert_file: c, key_file: k}\ndashboard: {enabled: true}\n"+minimalLogin), server)
			Expect(err).ToNot(HaveOccurred())
			Expect(server.Dashboard.Enabled).To(BeTrue())
		})
		It("should require tls", func() {
			err := yaml.Unmarshal([]byte("listen_addr: :9090\n"), &Server{})
			Expect(err).To(MatchError(ContainSubstring("tls is required")))
		})
		It("should accept a loopback_http_addr on a loopback address only", func() {
			for _, addr := range []string{"127.0.0.1:8480", "[::1]:8480", "localhost:8480"} {
				server := &Server{}
				err := yaml.Unmarshal([]byte("loopback_http_addr: \""+addr+"\"\ntls: {cert_file: c, key_file: k}\n"+minimalLogin), server)
				Expect(err).ToNot(HaveOccurred())
				Expect(server.LoopbackHttpAddr).To(Equal(addr))
			}
			for _, addr := range []string{":8480", "0.0.0.0:8480", "10.0.0.1:8480", "127.0.0.1"} {
				err := yaml.Unmarshal([]byte("loopback_http_addr: \""+addr+"\"\ntls: {cert_file: c, key_file: k}\n"+minimalLogin), &Server{})
				Expect(err).To(MatchError(ContainSubstring("must be on a loopback address")), addr)
			}
		})
		It("should require login", func() {
			err := yaml.Unmarshal([]byte("tls: {cert_file: c, key_file: k}\n"), &Server{})
			Expect(err).To(MatchError(ContainSubstring("login is required")))
		})
	})

	Context("ServerTls", func() {
		It("should require cert and key", func() {
			err := yaml.Unmarshal([]byte("cert_file: c\n"), &ServerTls{})
			Expect(err).To(MatchError(ContainSubstring("cert_file and key_file are required")))
		})
	})

	Context("ServerLogin", func() {
		It("should set lockout defaults", func() {
			login := readLogin("type: basic\nbasic_auth_options: [{username: u, password: p}]\n")

			Expect(login.LockoutAfterFailures).To(Equal(5))
			Expect(login.LockoutDuration).To(Equal(sdkconfig.Duration(5 * time.Minute)))
		})
		It("lowercases the type", func() {
			login := readLogin("type: LDAP\nldap_options: {bind_dn: cn=admin, bind_password: p, address: ldap:389}\n")
			Expect(login.Type).To(Equal("ldap"))
		})
		It("reads an oidc login with its defaults", func() {
			login := readLogin("type: oidc\noidc_options: {issuer_url: https://idp/r, client_id: flock}")
			Expect(login.Oidc.Scopes).To(Equal([]string{"openid", "profile", "email"}))
			Expect(login.Oidc.UsernameClaim).To(Equal("preferred_username"))
			Expect(login.Oidc.GroupsClaim).To(Equal("groups"))
			Expect(login.Oidc.AccessToken).To(BeNil())
		})
		It("always asks the openid scope", func() {
			login := readLogin("type: oidc\noidc_options: {issuer_url: https://idp/r, client_id: flock, scopes: [groups]}")
			Expect(login.Oidc.Scopes).To(Equal([]string{"openid", "groups"}))
		})
		It("defaults the username claim of access tokens to sub", func() {
			login := readLogin("type: oidc\noidc_options: {issuer_url: https://idp/r, client_id: flock, " +
				"access_token: {audience: flock-api}}")
			Expect(login.Oidc.AccessToken.Audience).To(Equal("flock-api"))
			Expect(login.Oidc.AccessToken.UsernameClaim).To(Equal("sub"))
		})
		It("requires the audience of access tokens", func() {
			err := yaml.Unmarshal([]byte("type: oidc\noidc_options: {issuer_url: https://idp/r, client_id: flock, "+
				"access_token: {}}"), &ServerLogin{})
			Expect(err).To(MatchError(ContainSubstring("oidc login: access_token.audience is required")))
		})
		DescribeTable("should refuse an incomplete oidc login",
			func(content string, errMsg string) {
				err := yaml.Unmarshal([]byte(content), &ServerLogin{})
				Expect(err).To(MatchError(ContainSubstring(errMsg)))
			},
			Entry("no issuer", "type: oidc\noidc_options: {client_id: flock}", "oidc login: issuer_url is required"),
			Entry("no client id", "type: oidc\noidc_options: {issuer_url: https://idp/r}", "oidc login: client_id is required"),
		)
		DescribeTable("should refuse an issuer that is not an absolute https url",
			func(issuer string, errMsg string) {
				err := yaml.Unmarshal([]byte("type: oidc\noidc_options: {issuer_url: \""+issuer+"\", client_id: flock}"), &ServerLogin{})
				Expect(err).To(MatchError(ContainSubstring(errMsg)))
			},
			Entry("relative", "idp.example.com/r", "must be an absolute https url"),
			Entry("another scheme", "ftp://idp.example.com/r", "must be an absolute https url"),
			Entry("without host", "https:///r", "must be an absolute https url"),
			Entry("plain http", "http://idp.example.com/r", "must use https, http is only accepted on the loopback"),
		)
		DescribeTable("should accept plain http on the loopback or when allowed",
			func(options string) {
				login := readLogin("type: oidc\noidc_options: {client_id: flock, " + options + "}")
				Expect(login.Oidc.IssuerUrl).To(HavePrefix("http://"))
			},
			Entry("localhost", "issuer_url: \"http://localhost:8080/r\""),
			Entry("127.0.0.1", "issuer_url: \"http://127.0.0.1:8080/r\""),
			Entry("::1", "issuer_url: \"http://[::1]:8080/r\""),
			Entry("insecure_http", "issuer_url: \"http://idp:8080/r\", insecure_http: true"),
		)
		It("refuses the username of break-glass tokens for a basic user", func() {
			err := yaml.Unmarshal([]byte("type: basic\nbasic_auth_options: [{username: break-glass, password: p}]\n"), &ServerLogin{})
			Expect(err).To(MatchError(ContainSubstring("basic auth login: username break-glass is reserved for break-glass tokens")))
		})
		It("requires a type", func() {
			err := yaml.Unmarshal([]byte("lockout_after_failures: 3\n"), &ServerLogin{})
			Expect(err).To(MatchError("server login: type is required"))
		})
		DescribeTable("should require the options of the type",
			func(content string, errMsg string) {
				err := yaml.Unmarshal([]byte(content), &ServerLogin{})
				Expect(err).To(MatchError(ContainSubstring(errMsg)))
			},
			Entry("basic without users", "type: basic\n", "server login: basic_auth_options needs at least one user"),
			Entry("ldap without options", "type: ldap\n", "server login: ldap_options is required"),
			Entry("oidc without options", "type: oidc\n", "server login: oidc_options is required"),
		)
		It("refuses the options of another type", func() {
			err := yaml.Unmarshal([]byte("type: ldap\nldap_options: {bind_dn: cn=admin, bind_password: p, address: ldap:389}\n"+
				"oidc_options: {issuer_url: https://idp/r, client_id: flock}\n"), &ServerLogin{})
			Expect(err).To(MatchError("server login: oidc_options is set but type is ldap"))
		})
		DescribeTable("refuses the removed types",
			func(loginType string) {
				err := yaml.Unmarshal([]byte("type: \""+loginType+"\"\n"), &ServerLogin{})
				Expect(err).To(MatchError(ContainSubstring("unknown type " + loginType + ", valid types are basic, ldap and oidc")))
			},
			Entry("null", "null"),
			Entry("oauth2", "oauth2"),
		)
	})

	Context("Server login and jwt maker", func() {
		const tlsYaml = "tls: {cert_file: c, key_file: k}\n"
		const basicLogin = "login: {type: basic, basic_auth_options: [{username: u, password: p}]}\n"

		It("requires jwt_maker", func() {
			err := yaml.Unmarshal([]byte(tlsYaml+basicLogin), &Server{})
			Expect(err).To(MatchError("server config: jwt_maker is required"))
		})
		It("refuses an oidc issuer equal to the jwt_maker issuer", func() {
			content := tlsYaml + "login: {type: oidc, oidc_options: {issuer_url: https://idp/r, client_id: flock}}\n" +
				"jwt_maker: {alg: HS256, private_key: secret, issuer: https://idp/r}\n"
			err := yaml.Unmarshal([]byte(content), &Server{})
			Expect(err).To(MatchError(ContainSubstring("oidc issuer_url must differ from jwt_maker.issuer")))
		})
		It("accepts an oidc issuer different from the jwt_maker issuer", func() {
			content := tlsYaml + "login: {type: oidc, oidc_options: {issuer_url: https://idp/r, client_id: flock}}\n" +
				"jwt_maker: {alg: HS256, private_key: secret}\n"
			server := &Server{}
			Expect(yaml.Unmarshal([]byte(content), server)).To(Succeed())
			Expect(server.JwtMaker.Issuer).To(Equal("flock"))
		})
	})
})

var _ = Describe("StoreManager", func() {
	Context("SqlStorer", func() {
		It("should set defaults and lower db type", func() {
			sql := &SqlStorer{}
			err := yaml.Unmarshal([]byte("type: SQLite\ndsn: file.db\nsecret_key: s\n"), sql)
			Expect(err).ToNot(HaveOccurred())

			Expect(sql.DbType).To(Equal("sqlite"))
			Expect(sql.CnxMaxIdle).To(Equal(10))
			Expect(sql.CnxMaxOpen).To(Equal(10))
			Expect(sql.CnxMaxLifetime).To(Equal(sdkconfig.Duration(time.Hour)))
			Expect(sql.CnxMaxIdleTime).To(Equal(sdkconfig.Duration(20 * time.Minute)))
		})
		DescribeTable("should reject sql storer missing field",
			func(content string, errMsg string) {
				err := yaml.Unmarshal([]byte(content), &SqlStorer{})
				Expect(err).To(MatchError(ContainSubstring(errMsg)))
			},
			Entry("no type", "dsn: d\nsecret_key: s\n", "type is required"),
			Entry("no dsn", "type: sqlite\nsecret_key: s\n", "dsn is required"),
			Entry("no secret", "type: sqlite\ndsn: d\n", "secret_key is required"),
		)
	})
})

var _ = Describe("Multi", func() {
	Context("NatsMultiOptions", func() {
		It("should set defaults", func() {
			natsOpts := &NatsMultiOptions{}
			err := yaml.Unmarshal([]byte("url: nats://localhost:4222\n"), natsOpts)
			Expect(err).ToNot(HaveOccurred())

			Expect(*natsOpts.MaxReconnect).To(Equal(60))
			Expect(natsOpts.ReconnectWait).To(Equal(sdkconfig.Duration(2 * time.Second)))
			Expect(natsOpts.ReconnectJitter).To(Equal(sdkconfig.Duration(100 * time.Millisecond)))
			Expect(natsOpts.ReconnectJitterTLS).To(Equal(sdkconfig.Duration(time.Second)))
			Expect(natsOpts.Timeout).To(Equal(sdkconfig.Duration(2 * time.Second)))
			Expect(natsOpts.DrainTimeout).To(Equal(sdkconfig.Duration(30 * time.Second)))
			Expect(natsOpts.FlusherTimeout).To(Equal(sdkconfig.Duration(2 * time.Second)))
			Expect(natsOpts.PingInterval).To(Equal(sdkconfig.Duration(2 * time.Minute)))
			Expect(natsOpts.MaxPingsOut).To(Equal(2))
			Expect(natsOpts.ReconnectBufSize).To(Equal(8 * 1024 * 1024))
			Expect(natsOpts.SubChanLen).To(Equal(65536))
		})
		It("should accept servers instead of url", func() {
			err := yaml.Unmarshal([]byte("servers: [nats://a:4222]\n"), &NatsMultiOptions{})
			Expect(err).ToNot(HaveOccurred())
		})
		It("should require url or servers", func() {
			err := yaml.Unmarshal([]byte("name: n\n"), &NatsMultiOptions{})
			Expect(err).To(MatchError(ContainSubstring("url or servers must be set")))
		})
		It("should copy values into nats options", func() {
			natsOpts := &NatsMultiOptions{}
			err := yaml.Unmarshal([]byte("url: nats://localhost:4222\nname: controller\nuser: u\npassword: p\nmax_reconnect: 3\nno_randomize: true\n"), natsOpts)
			Expect(err).ToNot(HaveOccurred())

			opts := &nats.Options{}
			err = natsOpts.ToOptions(opts)
			Expect(err).ToNot(HaveOccurred())

			Expect(opts.Url).To(Equal("nats://localhost:4222"))
			Expect(opts.Name).To(Equal("controller"))
			Expect(opts.User).To(Equal("u"))
			Expect(opts.Password).To(Equal("p"))
			Expect(opts.MaxReconnect).To(Equal(3))
			Expect(opts.NoRandomize).To(BeTrue())
			Expect(opts.ReconnectWait).To(Equal(2 * time.Second))
			Expect(opts.ClosedCB).ToNot(BeNil())
			Expect(opts.AsyncErrorCB).ToNot(BeNil())
		})
	})
})
