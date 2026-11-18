//go:build e2e

package dialects_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/seagalia/flock/controller/dbs"
	"github.com/seagalia/flock/controller/dbstore"
	"github.com/seagalia/flock/controller/lockers"
	"github.com/seagalia/flock/e2e/controller/env"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	"github.com/seagalia/flock/sdk/storers"
)

const (
	instanceUuid = "inst-1"
	agentUuid    = "agent-a"
	// both clocks are compared, the database's and the host's, which may drift a little under colima
	clockSkew = 5 * time.Second
)

type nodeRow struct {
	name, addr, data string
	lastSeen         int64
}

type lockRow struct {
	owner     string
	expiresAt int64
}

type leaseRow struct {
	leaseId, holderUuid, holderName string
	expiresAt                       int64
}

func startDatabase(ctx context.Context, req testcontainers.ContainerRequest, port string) string {
	GinkgoHelper()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	DeferCleanup(func(ctx context.Context) { _ = testcontainers.TerminateContainer(c) })
	Expect(err).NotTo(HaveOccurred())
	host, err := c.Host(ctx)
	Expect(err).NotTo(HaveOccurred())
	mapped, err := c.MappedPort(ctx, port)
	Expect(err).NotTo(HaveOccurred())
	return fmt.Sprintf("%s:%s", host, mapped.Port())
}

func startMysql(ctx context.Context) string {
	addr := startDatabase(ctx, testcontainers.ContainerRequest{
		Image:        env.MysqlImage,
		ExposedPorts: []string{"3306/tcp"},
		Env: map[string]string{
			"MYSQL_ROOT_PASSWORD": "flock-root",
			"MYSQL_USER":          "flock",
			"MYSQL_PASSWORD":      "flock",
			"MYSQL_DATABASE":      "flock",
		},
		// the init script runs a first server without network, the port only opens on the final one
		WaitingFor: wait.ForAll(
			wait.ForLog("port: 3306  MySQL Community Server"),
			wait.ForListeningPort("3306/tcp"),
		).WithDeadline(3 * time.Minute),
	}, "3306/tcp")
	return "flock:flock@tcp(" + addr + ")/flock"
}

func startPostgres(ctx context.Context) string {
	addr := startDatabase(ctx, testcontainers.ContainerRequest{
		Image:        env.PostgresImage,
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "flock",
			"POSTGRES_PASSWORD": "flock",
			"POSTGRES_DB":       "flock",
		},
		// the init script runs a first server that also logs it is ready, then restarts it
		WaitingFor: wait.ForAll(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
			wait.ForListeningPort("5432/tcp"),
		).WithDeadline(3 * time.Minute),
	}, "5432/tcp")
	return "postgres://flock:flock@" + addr + "/flock?sslmode=disable"
}

// takeoverSpecs runs the takeover cases of the upserts on one database. Mysql evaluates the assignments of its
// ON DUPLICATE KEY UPDATE left to right, so a reorder of them shows here and nowhere in the sqlite unit suites.
func takeoverSpecs(dbType dbs.DbType, start func(ctx context.Context) string) {
	var (
		dbMan  *dbs.DbManager
		nodes  storers.NodeStore
		leases *lockers.Leases
	)

	BeforeAll(func(ctx SpecContext) {
		// the db and store managers register their collectors on the default registry, which refuses a second
		// database in the same process
		prometheus.DefaultRegisterer = prometheus.NewRegistry()
		dsn := start(ctx)
		managerCtx, stopManager := context.WithCancel(context.Background())
		DeferCleanup(stopManager)
		var err error
		dbMan, err = dbs.NewDbManager(managerCtx, dbType, dsn, time.Hour, time.Hour, 10, 10)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(dbMan.Close)
		storeMan, err := dbstore.NewDbStoreManager(managerCtx, dbMan, "secret")
		Expect(err).NotTo(HaveOccurred())
		nodes = storeMan.Nodes()
		leases, err = lockers.NewLeases(ctx, dbMan)
		Expect(err).NotTo(HaveOccurred())
		_, err = lockers.NewDb(ctx, dbMan, lockers.DefaultTTL)
		Expect(err).NotTo(HaveOccurred())
	}, NodeTimeout(5*time.Minute))

	BeforeEach(func(ctx SpecContext) {
		for _, table := range []string{"AgentNodes", lockers.TableLocks, lockers.TableLeases} {
			_, err := dbMan.Exec(ctx, dbs.MakeDbQuery("DELETE FROM "+table, nil))
			Expect(err).NotTo(HaveOccurred())
		}
	})

	claim := func(ctx context.Context, name, clientType, addr string, staleAfter time.Duration) error {
		node := &svclookup.InstanceNode{AgentUuid: agentUuid, Name: name, Node: &core.Node{ClientType: clientType}}
		return nodes.ClaimNode(ctx, instanceUuid, node, addr, staleAfter)
	}
	readNode := func(ctx context.Context) nodeRow {
		GinkgoHelper()
		var row nodeRow
		query := "SELECT name, addr, data, last_seen FROM AgentNodes WHERE instance_uuid = '" + instanceUuid +
			"' AND agent_uuid = '" + agentUuid + "'"
		Expect(dbMan.QueryRow(ctx, dbs.MakeDbQuery(query, nil)).Scan(&row.name, &row.addr, &row.data, &row.lastSeen)).
			To(Succeed())
		return row
	}
	readLock := func(ctx context.Context, subject string) lockRow {
		GinkgoHelper()
		var row lockRow
		query := "SELECT owner, expires_at FROM " + lockers.TableLocks + " WHERE subject = '" + subject + "'"
		Expect(dbMan.QueryRow(ctx, dbs.MakeDbQuery(query, nil)).Scan(&row.owner, &row.expiresAt)).To(Succeed())
		return row
	}
	insertLock := func(ctx context.Context, subject, owner string, expiresAt time.Time) {
		GinkgoHelper()
		query := fmt.Sprintf("INSERT INTO %s (subject, owner, expires_at) VALUES ('%s', '%s', %d)",
			lockers.TableLocks, subject, owner, expiresAt.UnixMilli())
		_, err := dbMan.Exec(ctx, dbs.MakeDbQuery(query, nil))
		Expect(err).NotTo(HaveOccurred())
	}
	readLease := func(ctx context.Context, subject string) leaseRow {
		GinkgoHelper()
		var row leaseRow
		query := "SELECT lease_id, holder_uuid, holder_name, expires_at FROM " + lockers.TableLeases +
			" WHERE subject = '" + subject + "'"
		Expect(dbMan.QueryRow(ctx, dbs.MakeDbQuery(query, nil)).
			Scan(&row.leaseId, &row.holderUuid, &row.holderName, &row.expiresAt)).To(Succeed())
		return row
	}

	It("takes over a stale node claimed from another address, every column", func(ctx SpecContext) {
		Expect(claim(ctx, "node-old", "type-old", "10.0.0.1", time.Hour)).To(Succeed())
		before := readNode(ctx)
		time.Sleep(20 * time.Millisecond)

		Expect(claim(ctx, "node-new", "type-new", "10.0.0.2", time.Millisecond)).To(Succeed())

		after := readNode(ctx)
		Expect(after.name).To(Equal("node-new"))
		Expect(after.addr).To(Equal("10.0.0.2"))
		Expect(after.data).To(ContainSubstring("type-new"))
		Expect(after.lastSeen).To(BeNumerically(">", before.lastSeen))
		Expect(time.UnixMilli(after.lastSeen)).To(BeTemporally("~", time.Now(), clockSkew))
	})

	It("refuses a fresh node claimed from another address and leaves it untouched", func(ctx SpecContext) {
		Expect(claim(ctx, "node-old", "type-old", "10.0.0.1", time.Hour)).To(Succeed())
		before := readNode(ctx)

		err := claim(ctx, "node-new", "type-new", "10.0.0.2", time.Hour)

		var held *storers.NodeHeldError
		Expect(errors.As(err, &held)).To(BeTrue(), "got %v", err)
		Expect(held.Addr).To(Equal("10.0.0.1"))
		Expect(readNode(ctx)).To(Equal(before))
	})

	It("refreshes the name, data and last seen of a node claimed again from its address", func(ctx SpecContext) {
		Expect(claim(ctx, "node-old", "type-old", "10.0.0.1", time.Hour)).To(Succeed())
		before := readNode(ctx)
		time.Sleep(20 * time.Millisecond)

		Expect(claim(ctx, "node-new", "type-new", "10.0.0.1", time.Hour)).To(Succeed())

		after := readNode(ctx)
		Expect(after.name).To(Equal("node-new"))
		Expect(after.addr).To(Equal("10.0.0.1"))
		Expect(after.data).To(ContainSubstring("type-new"))
		Expect(after.lastSeen).To(BeNumerically(">", before.lastSeen))
	})

	It("takes over an expired lock for another owner", func(ctx SpecContext) {
		insertLock(ctx, "expired", "crashed", time.UnixMilli(0))
		locker, err := lockers.NewDb(ctx, dbMan, time.Minute)
		Expect(err).NotTo(HaveOccurred())

		lockCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		Expect(locker.Lock(lockCtx, "expired")).To(Succeed())
		DeferCleanup(locker.Unlock, context.Background(), "expired")

		row := readLock(ctx, "expired")
		Expect(row.owner).NotTo(Equal("crashed"))
		Expect(time.UnixMilli(row.expiresAt)).To(BeTemporally("~", time.Now().Add(time.Minute), clockSkew))
	})

	It("refuses a held lock and leaves it untouched", func(ctx SpecContext) {
		insertLock(ctx, "held", "other", time.Now().Add(time.Hour))
		before := readLock(ctx, "held")
		locker, err := lockers.NewDb(ctx, dbMan, time.Minute)
		Expect(err).NotTo(HaveOccurred())

		lockCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		defer cancel()
		Expect(locker.Lock(lockCtx, "held")).To(MatchError(context.DeadlineExceeded))

		Expect(readLock(ctx, "held")).To(Equal(before))
	})

	It("takes over an expired lease for another holder, every column", func(ctx SpecContext) {
		first := lockers.LeaseHolder{Uuid: "uuid-1", Name: "prov-1"}
		second := lockers.LeaseHolder{Uuid: "uuid-2", Name: "prov-2"}
		expired, err := leases.Acquire(ctx, "subject", first, 50*time.Millisecond)
		Expect(err).NotTo(HaveOccurred())
		time.Sleep(100 * time.Millisecond)

		lease, err := leases.Acquire(ctx, "subject", second, time.Minute)

		Expect(err).NotTo(HaveOccurred())
		Expect(lease.Granted).To(BeTrue())
		row := readLease(ctx, "subject")
		Expect(row.leaseId).To(Equal(lease.Id))
		Expect(row.leaseId).NotTo(Equal(expired.Id))
		Expect(row.holderUuid).To(Equal(second.Uuid))
		Expect(row.holderName).To(Equal(second.Name))
		Expect(time.UnixMilli(row.expiresAt)).To(BeTemporally("~", time.Now().Add(time.Minute), clockSkew))
	})

	It("refuses a held lease and leaves it untouched", func(ctx SpecContext) {
		first := lockers.LeaseHolder{Uuid: "uuid-1", Name: "prov-1"}
		second := lockers.LeaseHolder{Uuid: "uuid-2", Name: "prov-2"}
		_, err := leases.Acquire(ctx, "subject", first, time.Minute)
		Expect(err).NotTo(HaveOccurred())
		before := readLease(ctx, "subject")

		lease, err := leases.Acquire(ctx, "subject", second, time.Minute)

		Expect(err).NotTo(HaveOccurred())
		Expect(lease.Granted).To(BeFalse())
		Expect(lease.Holder).To(Equal(first))
		Expect(readLease(ctx, "subject")).To(Equal(before))
	})
}

var _ = Describe("Takeover upserts", func() {
	Describe("on mysql", Ordered, func() { takeoverSpecs(dbs.DbTypeMysql, startMysql) })
	Describe("on postgres", Ordered, func() { takeoverSpecs(dbs.DbTypePostgres, startPostgres) })
})
