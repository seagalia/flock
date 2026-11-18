package lockers

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/huandu/go-sqlbuilder"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/controller/dbs"
	"github.com/seagalia/flock/sdk/logging"
)

const (
	TableLocks  = "locks"
	TableLeases = "leases"
	// DefaultTTL is how long a lock outlives a controller that stopped renewing it (crashed or cut off)
	DefaultTTL    = 30 * time.Second
	retryEvery    = 50 * time.Millisecond
	unlockTimeout = 5 * time.Second
)

type Db struct {
	dbMan *dbs.DbManager
	ttl   time.Duration
	local *Memory
	entry zerolog.Logger

	heldMutex sync.Mutex
	held      map[string]*heldLock
}

type heldLock struct {
	owner       string
	stopRenewal context.CancelFunc
	renewalDone chan struct{}
}

func NewDb(ctx context.Context, dbMan *dbs.DbManager, ttl time.Duration) (*Db, error) {
	d := &Db{
		dbMan: dbMan,
		ttl:   ttl,
		local: NewMemory(),
		entry: logging.Component("lockers"),
		held:  make(map[string]*heldLock),
	}
	table := dbMan.Build().NewCreateTableBuilder().
		CreateTable(TableLocks).
		IfNotExists().
		Define("subject", "VARCHAR(255)", "PRIMARY KEY").
		Define("owner", "VARCHAR(36)", "NOT NULL").
		Define("expires_at", "BIGINT", "NOT NULL")
	_, err := dbMan.Exec(ctx, dbs.MakeDbQuery(table.Build()))
	if err != nil {
		return nil, fmt.Errorf("failed to create table %s: %w", TableLocks, err)
	}
	return d, nil
}

func (d *Db) Lock(ctx context.Context, subject string) error {
	err := d.local.Lock(ctx, subject)
	if err != nil {
		return err
	}
	// an owner per lock, not per controller: a row left by an earlier lock of this controller is not ours
	owner := uuid.NewString()
	for {
		acquired, err := d.tryLock(ctx, subject, owner)
		if err != nil {
			d.local.Unlock(ctx, subject)
			return fmt.Errorf("lock %s: %w", subject, err)
		}
		if acquired {
			break
		}
		select {
		case <-ctx.Done():
			d.local.Unlock(ctx, subject)
			return fmt.Errorf("lock %s: %w", subject, ctx.Err())
		case <-time.After(retryEvery):
		}
	}

	// the renewal outlives the ctx of Lock, which may end before Unlock: it keeps its values and stops only on Unlock
	renewalCtx, stopRenewal := context.WithCancel(context.WithoutCancel(ctx))
	held := &heldLock{
		owner:       owner,
		stopRenewal: stopRenewal,
		renewalDone: make(chan struct{}),
	}
	go d.renew(renewalCtx, subject, held)

	d.heldMutex.Lock()
	d.held[subject] = held
	d.heldMutex.Unlock()
	return nil
}

// Unlock deletes the row on a ctx detached from the caller's: handlers defer it, their request may be gone, and a row
// left behind makes the other controllers wait the ttl
func (d *Db) Unlock(ctx context.Context, subject string) {
	d.heldMutex.Lock()
	held, ok := d.held[subject]
	delete(d.held, subject)
	d.heldMutex.Unlock()
	if !ok {
		d.entry.Error().Str("subject", subject).Msg("unlock of a subject not locked")
		return
	}
	held.stopRenewal()
	<-held.renewalDone

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), unlockTimeout)
	defer cancel()
	db := d.dbMan.Build().NewDeleteBuilder()
	db.DeleteFrom(TableLocks).Where(db.Equal("subject", subject), db.Equal("owner", held.owner))
	_, err := d.dbMan.Exec(ctx, dbs.MakeDbQuery(db.Build()))
	if err != nil {
		// the row expires anyway, other controllers wait for it until then
		d.entry.Error().Err(err).Str("subject", subject).Msg("could not delete lock")
	}
	d.local.Unlock(ctx, subject)
}

// tryLock takes over an expired row in the same upsert that inserts a missing one: a delete of the expired row first
// would lock the gap of a missing one on mysql, and concurrent tries then deadlock on their inserts. It runs in one
// transaction so a try cut between its upsert and its read rolls back instead of leaving a row nobody knows it holds,
// which every controller would wait the ttl for.
func (d *Db) tryLock(ctx context.Context, subject, owner string) (bool, error) {
	now := d.dbMan.NowMillisSQL()

	// a crashed controller never unlocks: its row is taken over once expired
	ib := d.dbMan.Build().NewInsertBuilder()
	ib.InsertInto(TableLocks).
		Cols("subject", "owner", "expires_at").
		Values(subject, owner, sqlbuilder.Raw(expiresAt(now, d.ttl)))
	insertQuery, insertArgs := ib.Build()
	upsertQuery := takeOverExpired(d.dbMan.DbType, insertQuery, TableLocks, now, "owner")

	// reading the owner back works the same on every database, unlike the affected rows of the upsert
	sb := d.dbMan.Build().NewSelectBuilder()
	sb.Select("owner").From(TableLocks).Where(sb.Equal("subject", subject))
	selectQuery, selectArgs := sb.Build()

	var currentOwner string
	err := d.dbMan.WithExecTx(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, upsertQuery, insertArgs...); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, selectQuery, selectArgs...).Scan(&currentOwner)
	})
	if err != nil {
		return false, err
	}
	return currentOwner == owner, nil
}

// renew pushes the expiry of a held lock forward until Unlock, so a slow holder keeps it.
func (d *Db) renew(ctx context.Context, subject string, held *heldLock) {
	defer close(held.renewalDone)
	ticker := time.NewTicker(d.ttl / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		result, err := d.renewOnce(ctx, subject, held.owner)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			d.entry.Error().Err(err).Str("subject", subject).Msg("could not renew lock")
			continue
		}
		renewed, err := result.RowsAffected()
		if err == nil && renewed == 0 {
			d.entry.Error().Str("subject", subject).Msg("lock expired while held, another controller may hold it")
		}
	}
}

// renewOnce is bounded by the renewal period, so a hung query never makes the renewal miss the following ticks
func (d *Db) renewOnce(ctx context.Context, subject, owner string) (sql.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, d.ttl/3)
	defer cancel()
	ub := d.dbMan.Build().NewUpdateBuilder()
	ub.Update(TableLocks).
		Set(ub.Assign("expires_at", sqlbuilder.Raw(expiresAt(d.dbMan.NowMillisSQL(), d.ttl)))).
		Where(ub.Equal("subject", subject), ub.Equal("owner", owner))
	return d.dbMan.Exec(ctx, dbs.MakeDbQuery(ub.Build()))
}

// takeOverExpired makes insertQuery, on a table keyed by subject, replace the holder columns and the expiry of an
// existing row only once that row expired.
func takeOverExpired(dbType dbs.DbType, insertQuery, table, nowMillis string, holderCols ...string) string {
	cols := slices.Concat(holderCols, []string{"expires_at"})
	// qualified, because the existing row and the inserted one (excluded, new) share the column names
	expired := table + ".expires_at < " + nowMillis
	assigns := make([]string, len(cols))
	switch dbType {
	case dbs.DbTypePostgres, dbs.DbTypeSqlite:
		for i, col := range cols {
			assigns[i] = col + " = excluded." + col
		}
		return insertQuery + " ON CONFLICT (subject) DO UPDATE SET " + strings.Join(assigns, ", ") + " WHERE " + expired
	case dbs.DbTypeMysql:
		for i, col := range cols {
			assigns[i] = fmt.Sprintf("%s = IF(%s, new.%s, %s.%s)", col, expired, col, table, col)
		}
		return insertQuery + " AS new ON DUPLICATE KEY UPDATE " + strings.Join(assigns, ", ")
	}
	return insertQuery
}

func expiresAt(nowMillis string, ttl time.Duration) string {
	return nowMillis + " + " + strconv.FormatInt(ttl.Milliseconds(), 10)
}
