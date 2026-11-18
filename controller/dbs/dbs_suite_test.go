package dbs_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/seagalia/flock/controller/dbs"
)

func TestDbs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Dbs Suite")
}

// dbMan is shared by all specs because each db manager registers prometheus collectors globally
var dbMan *DbManager

var _ = BeforeSuite(func() {
	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)
	dsn := filepath.Join(GinkgoT().TempDir(), "test.db")
	var err error
	dbMan, err = NewDbManager(ctx, DbTypeSqlite, dsn, time.Hour, time.Hour, 1, 1)
	Expect(err).ToNot(HaveOccurred())
})
