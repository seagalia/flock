package permissioners_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/controller/dbs"
	"github.com/seagalia/flock/controller/dbstore"
)

func TestPermissioners(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Permissioners Suite")
}

// dbMan and storeMan are shared by all specs because they register prometheus collectors globally
var (
	dbMan    *dbs.DbManager
	storeMan *dbstore.DbStoreManager
)

var _ = BeforeSuite(func() {
	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)
	dsn := filepath.Join(GinkgoT().TempDir(), "test.db")
	var err error
	dbMan, err = dbs.NewDbManager(ctx, dbs.DbTypeSqlite, dsn, time.Hour, time.Hour, 1, 1)
	Expect(err).ToNot(HaveOccurred())
	storeMan, err = dbstore.NewDbStoreManager(ctx, dbMan, "secret")
	Expect(err).ToNot(HaveOccurred())
})
