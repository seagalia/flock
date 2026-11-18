package app

import (
	"context"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/controller/config"
	"github.com/seagalia/flock/controller/dbs"
	"github.com/seagalia/flock/controller/ipams"
	"github.com/seagalia/flock/sdk/logging"
)

var _ = Describe("loadIpam", func() {
	newApp := func(ipamConf *config.IPAMConfig) *App {
		return &App{cnf: &config.Config{IPAM: ipamConf}, entry: logging.Component("app")}
	}

	// Config.UnmarshalYAML gives an empty ipam block to a config without one
	It("should use the internal ipam without ipam block", func() {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		dsn := filepath.Join(GinkgoT().TempDir(), "test.db")
		dbMan, err := dbs.NewDbManager(ctx, dbs.DbTypeSqlite, dsn, time.Hour, time.Hour, 1, 1)
		Expect(err).ToNot(HaveOccurred())
		app := newApp(&config.IPAMConfig{})
		app.dbMan = dbMan

		Expect(app.loadIpam(context.Background())).To(Succeed())
		Expect(app.ipam).To(BeAssignableToTypeOf(&ipams.Db{}))
	})

	It("should keep the ipam off when disabled", func() {
		app := newApp(&config.IPAMConfig{Disabled: true, Remote: &config.RemoteIPAMConfig{URL: "https://ipam.local"}})
		Expect(app.loadIpam(context.Background())).To(Succeed())
		Expect(app.ipam).To(BeNil())
	})

	It("should use a remote ipam whatever the store", func() {
		app := newApp(&config.IPAMConfig{Remote: &config.RemoteIPAMConfig{URL: "https://ipam.local"}})
		Expect(app.loadIpam(context.Background())).To(Succeed())
		Expect(app.ipam).To(BeAssignableToTypeOf(&ipams.Remote{}))
	})

	It("should refuse an invalid remote url", func() {
		app := newApp(&config.IPAMConfig{Remote: &config.RemoteIPAMConfig{URL: "ipam.local"}})
		Expect(app.loadIpam(context.Background())).To(MatchError(ContainSubstring("invalid remote IPAM URL")))
	})
})
