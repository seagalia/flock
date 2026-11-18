package dbstore_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/controller/dbs"
	. "github.com/seagalia/flock/controller/dbstore"
)

func TestDbstore(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Dbstore Suite")
}

// dbMan and storeMan are shared by all specs because they register prometheus collectors globally
var (
	dbMan    *dbs.DbManager
	storeMan *DbStoreManager
)

const testSecret = "my-secret"

var _ = BeforeSuite(func() {
	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)
	dsn := filepath.Join(GinkgoT().TempDir(), "test.db")
	var err error
	dbMan, err = dbs.NewDbManager(ctx, dbs.DbTypeSqlite, dsn, time.Hour, time.Hour, 1, 1)
	Expect(err).ToNot(HaveOccurred())
	storeMan, err = NewDbStoreManager(ctx, dbMan, testSecret)
	Expect(err).ToNot(HaveOccurred())
})

var _ = BeforeEach(func() {
	tables := []string{
		"ForeignResources", "Permissions", "Metadata",
		"Backend", "Instance", "VIP", "VirtualServer", "Certificate", "Secret", "Zone", "AgentNodes",
	}
	for _, table := range tables {
		_, err := dbMan.Exec(context.Background(), dbs.MakeDbQuery("DELETE FROM "+table, nil))
		Expect(err).ToNot(HaveOccurred())
	}
})
