package dbs_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/seagalia/flock/controller/dbs"
)

var _ = Describe("DbManager", func() {
	BeforeEach(func() {
		_, err := dbMan.Exec(context.Background(), MakeDbQuery("CREATE TABLE IF NOT EXISTS items (name VARCHAR(255) NOT NULL)", nil))
		Expect(err).ToNot(HaveOccurred())
		_, err = dbMan.Exec(context.Background(), MakeDbQuery("DELETE FROM items", nil))
		Expect(err).ToNot(HaveOccurred())
	})
	countItems := func() int {
		var count int
		err := dbMan.QueryRow(context.Background(), MakeDbQuery("SELECT COUNT(*) FROM items", nil)).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		return count
	}

	Context("NewDbManager", func() {
		It("should refuse unsupported db type", func() {
			_, err := NewDbManager(context.Background(), DbType("oracle"), "dsn", time.Hour, time.Hour, 1, 1)
			Expect(err).To(MatchError(ContainSubstring("unsupported db type: oracle")))
		})
		It("should give sql flavor of the db type", func() {
			Expect(dbMan.DbType).To(Equal(DbTypeSqlite))
			Expect(dbMan.Build().String()).To(Equal("SQLite"))
		})
	})

	Context("Ping", func() {
		It("should succeed while the database answers", func() {
			Expect(dbMan.Ping(context.Background())).To(Succeed())
		})
	})

	Context("Exec and Query", func() {
		It("should insert and query rows with args", func() {
			_, err := dbMan.Exec(context.Background(), MakeDbQuery("INSERT INTO items (name) VALUES (?), (?)", []any{"a", "b"}))
			Expect(err).ToNot(HaveOccurred())

			rows, err := dbMan.Query(context.Background(), MakeDbQuery("SELECT name FROM items WHERE name = ?", []any{"b"}))
			Expect(err).ToNot(HaveOccurred())
			defer rows.Close()
			names := make([]string, 0)
			for rows.Next() {
				var name string
				Expect(rows.Scan(&name)).To(Succeed())
				names = append(names, name)
			}
			Expect(names).To(Equal([]string{"b"}))
		})
	})

	Context("ExecTx", func() {
		It("should commit all queries", func() {
			results, err := dbMan.ExecTx(context.Background(), []DbQuery{
				MakeDbQuery("INSERT INTO items (name) VALUES (?)", []any{"a"}),
				MakeDbQuery("INSERT INTO items (name) VALUES (?)", []any{"b"}),
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(results).To(HaveLen(2))
			Expect(countItems()).To(Equal(2))
		})
		It("should rollback all queries when one fails", func() {
			_, err := dbMan.ExecTx(context.Background(), []DbQuery{
				MakeDbQuery("INSERT INTO items (name) VALUES (?)", []any{"a"}),
				MakeDbQuery("INSERT INTO unknown_table (name) VALUES (?)", []any{"b"}),
			})
			Expect(err).To(HaveOccurred())
			Expect(countItems()).To(Equal(0))
		})
	})

	Context("with a cancelled ctx", func() {
		var ctx context.Context
		BeforeEach(func() {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(context.Background())
			cancel()
		})

		It("should fail Exec at once without retrying", func() {
			start := time.Now()
			_, err := dbMan.Exec(ctx, MakeDbQuery("INSERT INTO items (name) VALUES (?)", []any{"a"}))
			Expect(err).To(MatchError(context.Canceled))
			Expect(time.Since(start)).To(BeNumerically("<", 500*time.Millisecond))
			Expect(countItems()).To(Equal(0))
		})
		It("should fail Query at once without retrying", func() {
			start := time.Now()
			_, err := dbMan.Query(ctx, MakeDbQuery("SELECT name FROM items", nil))
			Expect(err).To(MatchError(context.Canceled))
			Expect(time.Since(start)).To(BeNumerically("<", 500*time.Millisecond))
		})
		It("should fail ExecTx at once and store nothing", func() {
			start := time.Now()
			_, err := dbMan.ExecTx(ctx, []DbQuery{
				MakeDbQuery("INSERT INTO items (name) VALUES (?)", []any{"a"}),
				MakeDbQuery("INSERT INTO items (name) VALUES (?)", []any{"b"}),
			})
			Expect(err).To(MatchError(context.Canceled))
			Expect(time.Since(start)).To(BeNumerically("<", 500*time.Millisecond))
			Expect(countItems()).To(Equal(0))
		})
		It("should fail QueryRow's Scan", func() {
			var count int
			err := dbMan.QueryRow(ctx, MakeDbQuery("SELECT COUNT(*) FROM items", nil)).Scan(&count)
			Expect(err).To(MatchError(context.Canceled))
		})
	})
})
