package dbs

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	sqldblogger "github.com/simukti/sqldb-logger"
)

type recordedLevels []sqldblogger.Level

func (r *recordedLevels) Log(_ context.Context, level sqldblogger.Level, _ string, _ map[string]any) {
	*r = append(*r, level)
}

var _ = Describe("DbManager internals", func() {
	It("should log the error of a query cut by its ctx at debug, the error of a live query at error", func() {
		recorded := &recordedLevels{}
		logger := cancelAwareLogger{next: recorded}
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()

		logger.Log(cancelled, sqldblogger.LevelError, "ExecContext", nil)
		logger.Log(context.Background(), sqldblogger.LevelError, "ExecContext", nil)

		Expect(*recorded).To(Equal(recordedLevels{sqldblogger.LevelDebug, sqldblogger.LevelError}))
	})

	It("should lock sqlite transactions at their start and wait on a busy database", func() {
		dsn := configureSQLiteDSN("/tmp/store.db?_pragma=busy_timeout(10)&_pragma=foreign_keys(1)")

		u, err := url.Parse(dsn)
		Expect(err).ToNot(HaveOccurred())
		Expect(u.Scheme).To(Equal("file"))
		Expect(u.Query().Get("_txlock")).To(Equal("immediate"))
		Expect(u.Query()["_pragma"]).To(ConsistOf("foreign_keys(1)", "busy_timeout(5000)"))
	})

	DescribeTable("should retry transactions on lock and serialization errors only",
		func(isRetryable func(error) bool, err error, expected bool) {
			Expect(isRetryable(fmt.Errorf("exec: %w", err))).To(Equal(expected))
		},
		Entry("postgres serialization", isRetryableTxErrorPostgres, &pgconn.PgError{Code: "40001"}, true),
		Entry("postgres deadlock", isRetryableTxErrorPostgres, &pgconn.PgError{Code: "40P01"}, true),
		Entry("postgres unique violation", isRetryableTxErrorPostgres, &pgconn.PgError{Code: "23505"}, false),
		Entry("mysql deadlock", isRetryableTxErrorMysql, &mysql.MySQLError{Number: 1213}, true),
		Entry("mysql duplicate", isRetryableTxErrorMysql, &mysql.MySQLError{Number: 1062}, false),
		Entry("sqlite other error", isRetryableTxErrorSqlite, fmt.Errorf("disk full"), false),
	)
})

var _ = Describe("DbManager lifetime", func() {
	// each db manager registers its collector by name, a registry per spec lets several live in the process
	newDbManager := func(ctx context.Context) *DbManager {
		previous := prometheus.DefaultRegisterer
		prometheus.DefaultRegisterer = prometheus.NewRegistry()
		DeferCleanup(func() { prometheus.DefaultRegisterer = previous })

		dsn := filepath.Join(GinkgoT().TempDir(), "lifetime.db")
		d, err := NewDbManager(ctx, DbTypeSqlite, dsn, time.Hour, time.Hour, 1, 1)
		Expect(err).ToNot(HaveOccurred())
		return d
	}

	It("should return from Close once the database watch has stopped", func() {
		d := newDbManager(context.Background())

		Expect(d.Close()).To(Succeed())

		Expect(d.watchDone).To(BeClosed())
	})

	It("should stop only the watch when its context is cancelled, queries run on their own", func() {
		ctx, cancel := context.WithCancel(context.Background())
		d := newDbManager(ctx)
		DeferCleanup(d.Close)
		cancel()

		Eventually(d.watchDone).Should(BeClosed())
		_, err := d.Exec(context.Background(), MakeDbQuery("SELECT 1", nil))
		Expect(err).ToNot(HaveOccurred())
	})

	It("should read the database of QueryRow under the reconnect lock", func() {
		d := newDbManager(context.Background())
		DeferCleanup(d.Close)

		stop := make(chan struct{})
		queriesDone := make(chan struct{})
		go func() {
			defer close(queriesDone)
			for {
				select {
				case <-stop:
					return
				default:
				}
				var one int
				_ = d.QueryRow(context.Background(), MakeDbQuery("SELECT 1", nil)).Scan(&one) // nolint:errcheck
			}
		}()
		for range 20 {
			Expect(d.connectDB()).To(Succeed())
		}
		close(stop)
		Eventually(queriesDone).Should(BeClosed())
	})
})
