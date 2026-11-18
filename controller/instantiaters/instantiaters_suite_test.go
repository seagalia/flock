package instantiaters_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/controller/dbs"
)

func TestInstantiaters(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Instantiaters Suite")
}

// dbMan is shared by all specs because each db manager registers prometheus collectors globally
var dbMan *dbs.DbManager

var _ = BeforeSuite(func() {
	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)
	dsn := filepath.Join(GinkgoT().TempDir(), "test.db")
	var err error
	dbMan, err = dbs.NewDbManager(ctx, dbs.DbTypeSqlite, dsn, time.Hour, time.Hour, 1, 1)
	Expect(err).ToNot(HaveOccurred())
})
