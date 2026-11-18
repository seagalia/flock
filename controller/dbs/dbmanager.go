package dbs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/avast/retry-go/v5"
	"github.com/go-sql-driver/mysql"
	"github.com/huandu/go-sqlbuilder"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/rs/zerolog"
	zlog "github.com/rs/zerolog/log"
	"github.com/seagalia/flock/sdk/logging"
	"github.com/simukti/sqldb-logger"
	"github.com/simukti/sqldb-logger/logadapter/zerologadapter"
	"modernc.org/sqlite"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

const (
	DbTypePostgres DbType = "postgres"
	DbTypeMysql    DbType = "mysql"
	DbTypeSqlite   DbType = "sqlite"
)

const (
	watchInterval = 2 * time.Minute
	// watchPingTimeout bounds the ping of a database that stopped answering, the watch would hang on it
	watchPingTimeout = 5 * time.Second
	// watchReconnectDelay spaces the reconnections of a database that can't be opened
	watchReconnectDelay = 5 * time.Second
)

// cancelAwareLogger logs at debug the driver error of a query whose ctx is done: a client gone or a stop is not a
// database failure
type cancelAwareLogger struct {
	next sqldblogger.Logger
}

func (l cancelAwareLogger) Log(ctx context.Context, level sqldblogger.Level, msg string, data map[string]any) {
	if level == sqldblogger.LevelError && ctx.Err() != nil {
		level = sqldblogger.LevelDebug
	}
	l.next.Log(ctx, level, msg, data)
}

type sqlStateError interface {
	SQLState() string
}

func isRetryableTxErrorSqlite(err error) bool {
	if sqliteErr, ok := errors.AsType[*sqlite.Error](err); ok {
		switch sqliteErr.Code() {
		case 5, // SQLITE_BUSY
			6,   // SQLITE_LOCKED
			261, // SQLITE_BUSY_RECOVERY
			262, // SQLITE_LOCKED_SHAREDCACHE
			517: // SQLITE_BUSY_SNAPSHOT
			return true
		}
	}

	return false
}

func isRetryableTxErrorPostgres(err error) bool {
	var pgErr sqlStateError
	if errors.As(err, &pgErr) {
		switch pgErr.SQLState() {
		case "40001", // serialization_failure
			"40P01": // deadlock_detected
			return true
		}
	}

	return false
}

func isRetryableTxErrorMysql(err error) bool {
	if mysqlErr, ok := errors.AsType[*mysql.MySQLError](err); ok {
		switch mysqlErr.Number {
		case 1213, // ER_LOCK_DEADLOCK
			1205: // ER_LOCK_WAIT_TIMEOUT
			return true
		}
	}
	return false
}

type DbQuery struct {
	query string
	args  []any
}

func MakeDbQuery(q string, args []any) DbQuery {
	return DbQuery{
		query: q,
		args:  args,
	}
}

type DbType string

type DbManager struct {
	db                 *sql.DB
	entry              zerolog.Logger
	driverName         string
	connStr            string
	connMaxLifetime    time.Duration
	connMaxIdleTime    time.Duration
	maxIdleConns       int
	maxOpensConns      int
	reconRwMutex       *sync.RWMutex
	ctx                context.Context
	cancel             context.CancelFunc
	statCollector      prometheus.Collector
	dbFlavor           sqlbuilder.Flavor
	DbType             DbType
	isRetryableTxError func(error) bool
	watchDone          chan struct{}
}

func NewDbManager(
	ctx context.Context,
	dbType DbType, connStr string,
	connMaxLifetime, connMaxIdleTime time.Duration,
	maxIdleConns, maxOpensConns int,
) (*DbManager, error) {
	driverName := string(dbType)
	var dbFlavor sqlbuilder.Flavor
	var isRetryableTxError func(error) bool
	switch dbType {
	case DbTypePostgres:
		dbFlavor = sqlbuilder.PostgreSQL
		driverName = "pgx"
		isRetryableTxError = isRetryableTxErrorPostgres
	case DbTypeMysql:
		dbFlavor = sqlbuilder.MySQL
		isRetryableTxError = isRetryableTxErrorMysql
	case DbTypeSqlite:
		dbFlavor = sqlbuilder.SQLite
		isRetryableTxError = isRetryableTxErrorSqlite
		connStr = configureSQLiteDSN(connStr)
	default:
		return nil, fmt.Errorf("unsupported db type: %s", dbType)
	}
	// the database outlives the stop signal of the app: calls in flight during the drain still need it, Close ends it
	ctx, cancel := context.WithCancel(ctx)
	dsm := &DbManager{
		entry:              logging.Component("dbmanager"),
		driverName:         driverName,
		connStr:            connStr,
		connMaxLifetime:    connMaxLifetime,
		connMaxIdleTime:    connMaxIdleTime,
		reconRwMutex:       &sync.RWMutex{},
		maxIdleConns:       maxIdleConns,
		maxOpensConns:      maxOpensConns,
		ctx:                ctx,
		cancel:             cancel,
		dbFlavor:           dbFlavor,
		DbType:             dbType,
		isRetryableTxError: isRetryableTxError,
		watchDone:          make(chan struct{}),
	}
	err := dsm.connectDB()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to connect to db: %s", err.Error())
	}
	go dsm.watchDB()
	return dsm, nil
}

// Close stops the watch before closing the database, a watch seeing a closed database would open it again
func (d *DbManager) Close() error {
	d.cancel()
	<-d.watchDone
	d.reconRwMutex.Lock()
	defer d.reconRwMutex.Unlock()
	prometheus.Unregister(d.statCollector)
	return d.db.Close()
}

// Ping checks the database answers, without reconnecting: watchDB does it
func (d *DbManager) Ping(ctx context.Context) error {
	d.reconRwMutex.RLock()
	defer d.reconRwMutex.RUnlock()
	return d.db.PingContext(ctx)
}

func (d *DbManager) Build() sqlbuilder.Flavor {
	return d.dbFlavor
}

func (d *DbManager) watchDB() {
	defer close(d.watchDone)
	entry := d.entry.With().Str("method", "watchDB").Logger()
	for {
		pingCtx, cancel := context.WithTimeout(d.ctx, watchPingTimeout)
		err := d.Ping(pingCtx)
		cancel()
		if d.ctx.Err() != nil {
			entry.Debug().Msg("stop watching the database")
			return
		}
		wait := watchInterval
		if err != nil {
			stats.SetDbDown()
			entry.Error().Msgf("failed to ping database, try reconnect: %s", err.Error())
			err = d.connectDB()
		}
		if err != nil {
			entry.Error().Msgf("failed to reconnect database, retrying in %s: %s", watchReconnectDelay, err.Error())
			wait = watchReconnectDelay
		}
		select {
		case <-time.After(wait):
		case <-d.ctx.Done():
			entry.Debug().Msg("stop watching the database")
			return
		}
	}
}

func (d *DbManager) connectDB() error {
	d.reconRwMutex.Lock()
	defer d.reconRwMutex.Unlock()
	if d.db != nil {
		prometheus.Unregister(d.statCollector)
		d.db.Close() // nolint
	}
	// a failed open keeps the closed database, a nil one would make the next ping panic
	opened, err := sql.Open(d.driverName, d.connStr)
	if err != nil {
		return err
	}
	d.db = opened
	loggerAdapter := cancelAwareLogger{next: zerologadapter.New(zlog.Logger)}
	d.db = sqldblogger.OpenDriver(
		d.connStr, d.db.Driver(),
		loggerAdapter,
		sqldblogger.WithMinimumLevel(sqldblogger.LevelTrace),
		sqldblogger.WithPreparerLevel(sqldblogger.LevelDebug),
		sqldblogger.WithQueryerLevel(sqldblogger.LevelDebug),
		sqldblogger.WithExecerLevel(sqldblogger.LevelDebug),
	)

	d.db.SetConnMaxLifetime(d.connMaxLifetime)
	d.db.SetConnMaxIdleTime(d.connMaxIdleTime)
	d.db.SetMaxOpenConns(d.maxOpensConns)
	d.db.SetMaxIdleConns(d.maxIdleConns)

	stats.SetDbUp()
	d.statCollector = collectors.NewDBStatsCollector(d.db, "flock_db")
	prometheus.MustRegister(d.statCollector)
	return nil
}

func (d *DbManager) Exec(ctx context.Context, q DbQuery) (sql.Result, error) {
	var result sql.Result
	var err error
	retryErr := d.queryRetrier(ctx).Do(func() error {
		d.reconRwMutex.RLock()
		defer d.reconRwMutex.RUnlock()
		result, err = d.db.ExecContext(ctx, q.query, q.args...)
		if err != nil {
			return fmt.Errorf("failed to Exec Query: %w", err)
		}
		return nil
	})

	return result, retryErr
}

func (d *DbManager) ExecTx(ctx context.Context, qs []DbQuery) ([]sql.Result, error) {
	var results []sql.Result

	err := retry.New(
		retry.Attempts(3),
		retry.Delay(100*time.Millisecond),
		retry.MaxDelay(time.Second),
		retry.Context(ctx),
		retry.LastErrorOnly(true),
		retry.RetryIf(d.isRetryableTxError),
	).Do(func() error {
		d.reconRwMutex.RLock()
		defer d.reconRwMutex.RUnlock()

		tx, err := d.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		// after a commit it only gives sql.ErrTxDone
		defer func() { _ = tx.Rollback() }()

		attemptResults := make([]sql.Result, len(qs))

		for i, q := range qs {
			result, err := tx.ExecContext(
				ctx,
				q.query,
				q.args...,
			)
			if err != nil {
				return err
			}

			attemptResults[i] = result
		}

		if err := tx.Commit(); err != nil {
			return retry.Unrecoverable(
				fmt.Errorf("commit failed: %w", err),
			)
		}

		results = attemptResults
		return nil
	})

	if err != nil {
		return nil, err
	}

	return results, nil
}

func (d *DbManager) WithExecTx(
	ctx context.Context,
	opts *sql.TxOptions,
	fn func(*sql.Tx) error,
) error {
	return retry.New(
		retry.Attempts(3),
		retry.Delay(100*time.Millisecond),
		retry.RetryIf(d.isRetryableTxError),
		retry.Context(ctx),
		retry.LastErrorOnly(true),
	).Do(func() error {
		d.reconRwMutex.RLock()
		defer d.reconRwMutex.RUnlock()

		tx, err := d.db.BeginTx(ctx, opts)
		if err != nil {
			return err
		}

		// after a commit it only gives sql.ErrTxDone
		defer func() { _ = tx.Rollback() }()

		if err := fn(tx); err != nil {
			return err
		}

		if err := tx.Commit(); err != nil {
			return retry.Unrecoverable(err)
		}

		return nil
	})
}

func (d *DbManager) Query(ctx context.Context, q DbQuery) (*sql.Rows, error) {
	var result *sql.Rows
	var err error
	retryErr := d.queryRetrier(ctx).Do(func() error {
		d.reconRwMutex.RLock()
		defer d.reconRwMutex.RUnlock()
		result, err = d.db.QueryContext(ctx, q.query, q.args...)
		if err != nil {
			return fmt.Errorf("failed to Exec Query: %w", err)
		}
		return nil
	})

	return result, retryErr
}

func (d *DbManager) QueryRow(ctx context.Context, q DbQuery) *sql.Row {
	d.reconRwMutex.RLock()
	defer d.reconRwMutex.RUnlock()
	return d.db.QueryRowContext(ctx, q.query, q.args...)
}

// queryRetrier gives a failed query two more tries, none to a cancelled one since it would fail the same way again
func (d *DbManager) queryRetrier(ctx context.Context) *retry.Retrier {
	return retry.New(
		retry.Attempts(3),
		retry.Delay(1*time.Second),
		retry.Context(ctx),
		retry.LastErrorOnly(true),
		retry.RetryIf(isRetryableQueryError),
	)
}

func isRetryableQueryError(err error) bool {
	isCancelled := errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
	return !isCancelled
}

func configureSQLiteDSN(dsn string) string {
	if !strings.HasPrefix(dsn, "file:") {
		dsn = "file:" + dsn
	}

	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	q := u.Query()
	q.Set("_txlock", "immediate")
	pragmas := q["_pragma"]
	q.Del("_pragma")
	for _, pragma := range pragmas {
		if !strings.HasPrefix(
			strings.ToLower(pragma),
			"busy_timeout(",
		) {
			q.Add("_pragma", pragma)
		}
	}
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	return u.String()
}

// NowMillisSQL is an sql expression of the database time in milliseconds. Controllers share rows with time limits, so
// they compare against the clock of the database, never their own.
func (d *DbManager) NowMillisSQL() string {
	switch d.DbType {
	case DbTypePostgres:
		return "CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)"
	case DbTypeMysql:
		// UNIX_TIMESTAMP(NOW()) follows the session time zone and jumps with daylight saving time
		return "CAST(TIMESTAMPDIFF(MICROSECOND, '1970-01-01 00:00:00', UTC_TIMESTAMP(6)) DIV 1000 AS SIGNED)"
	case DbTypeSqlite:
		return "CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER)"
	default:
		panic("unsupported database backend")
	}
}
