//go:build e2e

package env_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/controller/config"
	"github.com/seagalia/flock/e2e/controller/env"
)

var _ = Describe("RenderControllerConfig", func() {
	var certs *env.Certs
	var dir string

	BeforeEach(func() {
		var err error
		certs, err = env.NewCerts("controller-sqlite")
		Expect(err).NotTo(HaveOccurred())
		dir = GinkgoT().TempDir()
		Expect(certs.WriteFiles(dir)).To(Succeed())
	})

	readRendered := func(o env.ControllerOptions) *config.Config {
		GinkgoHelper()
		rendered, err := env.RenderControllerConfig(o, certs, dir)
		Expect(err).NotTo(HaveOccurred())
		path := filepath.Join(dir, "config.yml")
		Expect(os.WriteFile(path, rendered, 0o600)).To(Succeed())
		conf, err := config.ReadConfigStrict(path)
		Expect(err).NotTo(HaveOccurred(), "rendered config:\n%s", rendered)
		return conf
	}

	It("logs at debug unless told otherwise", func() {
		Expect(readRendered(env.ControllerOptions{Name: "controller-sqlite", Store: env.StoreSqlite}).Log.Level).To(Equal("debug"))
		infoOpts := env.ControllerOptions{Name: "controller-sqlite", Store: env.StoreSqlite, LogLevel: "info"}
		Expect(readRendered(infoOpts).Log.Level).To(Equal("info"))
	})

	DescribeTable("gives a config controller accepts",
		func(store env.Store, natsURL string) {
			dsns := map[env.Store]string{
				env.StorePostgres: "postgres://flock:flock@postgres-1:5432/flock?sslmode=disable",
				env.StoreMysql:    "flock:flock@tcp(mysql-1:3306)/flock",
			}
			dsn := dsns[store]
			conf := readRendered(env.ControllerOptions{Name: "controller-sqlite", Store: store, DSN: dsn, NatsURL: natsURL})

			Expect(conf.Server.ListenAddr).To(Equal("0.0.0.0:8443"))
			Expect(conf.Server.Tls.CertFile).To(Equal(filepath.Join(dir, "controller.pem")))
			Expect(conf.Server.AdminConfig.Users).To(ConsistOf("admin"))
			Expect(conf.Server.Login.Type).To(Equal("basic"))
			Expect(conf.Server.Login.BasicAuth).To(ConsistOf(HaveField("Username", "admin")))
			Expect(conf.Server.JwtMaker.Issuer).To(Equal("controller-e2e"))
			Expect(conf.Mtls.ServerName).To(Equal("flock_agent"))

			Expect(conf.StoreManager.SqlStorer.DbType).To(Equal(string(store)))
			if store == env.StoreSqlite {
				Expect(conf.StoreManager.SqlStorer.DSN).To(Equal("/tmp/flock.db"))
			} else {
				Expect(conf.StoreManager.SqlStorer.DSN).To(Equal(dsn))
			}

			if natsURL == "" {
				Expect(conf.Multi).To(BeNil())
				return
			}
			Expect(conf.Multi).NotTo(BeNil())
			Expect(conf.Multi.Nats.Url).To(Equal(natsURL))
		},
		Entry("sqlite", env.StoreSqlite, ""),
		Entry("sqlite with nats", env.StoreSqlite, "nats://nats-1:4222"),
		Entry("postgres", env.StorePostgres, ""),
		Entry("postgres with nats", env.StorePostgres, "nats://nats-1:4222"),
		Entry("mysql", env.StoreMysql, ""),
	)

	It("refuses a mysql store without dsn", func() {
		_, err := env.RenderControllerConfig(env.ControllerOptions{Name: "controller-sqlite", Store: env.StoreMysql}, certs, dir)
		Expect(err).To(MatchError(ContainSubstring("needs a dsn")))
	})

	It("logs in with ldap, and adds the ipam and the dashboard", func() {
		conf := readRendered(env.ControllerOptions{
			Name:      "controller-sqlite",
			Store:     env.StoreSqlite,
			Ldap:      &env.Ldap{Name: "ldap-1"},
			Ipam:      map[string]any{"remote": map[string]any{"url": "http://host.testcontainers.internal:9000"}},
			Dashboard: true,
		})

		Expect(conf.Server.Login.Type).To(Equal("ldap"))
		Expect(conf.Server.Login.BasicAuth).To(BeEmpty())
		Expect(conf.Server.Login.Ldap.Address).To(Equal("ldap-1:389"))
		Expect(conf.Server.Login.Ldap.GroupSearchFilter).To(Equal("(member=%s)"))
		Expect(conf.IPAM.Remote).NotTo(BeNil())
		Expect(conf.IPAM.Remote.URL).To(Equal("http://host.testcontainers.internal:9000"))
		Expect(conf.Server.Dashboard.Enabled).To(BeTrue())
	})

	It("declares the subscriber in its zone", func() {
		conf := readRendered(env.ControllerOptions{
			Name: "controller-sqlite", Store: env.StoreSqlite, Instantiators: env.SubscriberInstantiators(),
		})

		Expect(conf.Instantiators).To(HaveLen(1))
		Expect(conf.Instantiators[0].Zones).To(ConsistOf(env.SubscriberZone))
		Expect(conf.Instantiators[0].ResourceTypes).To(ConsistOf(HaveField("Name", "small")))
		Expect(conf.Instantiators[0].StaticInstantiator.Agents).To(ConsistOf(SatisfyAll(
			HaveField("Name", env.SubscriberName),
			HaveField("UUID", env.SubscriberUuid),
		)))
	})

	It("renders instantiators as given", func() {
		conf := readRendered(env.ControllerOptions{
			Name:  "controller-sqlite",
			Store: env.StoreSqlite,
			Instantiators: []map[string]any{{
				"name":           "static",
				"zones":          []string{"z1"},
				"resource_types": []map[string]any{{"name": "small"}},
				"static": map[string]any{"agents": []map[string]any{{
					"uuid": "agent-1", "name": "agent-1", "host": "host.testcontainers.internal:9000",
				}}},
			}},
		})
		Expect(conf.Instantiators).To(HaveLen(1))
		Expect(conf.Instantiators[0].Name).To(Equal("static"))
	})

	It("logs in with the oidc provider, its ID tokens and its access tokens", func() {
		conf := readRendered(env.ControllerOptions{
			Name:  "controller-sqlite",
			Store: env.StoreSqlite,
			Oidc:  &env.Oauth2{Name: "oauth2-1"},
		})

		Expect(conf.Server.Login.Type).To(Equal("oidc"))
		Expect(conf.Server.Login.BasicAuth).To(BeEmpty())
		oidc := conf.Server.Login.Oidc
		Expect(oidc.IssuerUrl).To(Equal("http://oauth2-1:8080/default"))
		Expect(oidc.ClientId).To(Equal("flock"))
		Expect(oidc.AccessToken.Audience).To(Equal("flock-api"))
		Expect(conf.Server.JwtMaker.Issuer).To(Equal("controller-e2e"))
	})

	It("refuses ldap and oidc together, a controller has one login service", func() {
		_, err := env.RenderControllerConfig(env.ControllerOptions{
			Name:  "controller-sqlite",
			Store: env.StoreSqlite,
			Ldap:  &env.Ldap{Name: "ldap-1"},
			Oidc:  &env.Oauth2{Name: "oauth2-1"},
		}, certs, dir)
		Expect(err).To(MatchError(ContainSubstring("one login service")))
	})
})
