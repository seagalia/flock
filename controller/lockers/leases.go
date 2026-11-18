package lockers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/huandu/go-sqlbuilder"
	"github.com/seagalia/flock/controller/dbs"
)

var ErrLeaseLost = errors.New("lease lost")

type LeaseHolder struct {
	Uuid string
	Name string
}

type Lease struct {
	Granted bool
	// Id is set when granted
	Id string
	// Holder is the holder of the row, the caller when granted
	Holder    LeaseHolder
	ExpiresAt time.Time
}

// Leases grants a subject to one holder at a time, on the clock of the database. Unlike Db it holds nothing for the
// caller: the holder renews its lease and a crashed one is taken over once the lease expired.
type Leases struct {
	dbMan *dbs.DbManager
}

func NewLeases(ctx context.Context, dbMan *dbs.DbManager) (*Leases, error) {
	table := dbMan.Build().NewCreateTableBuilder().
		CreateTable(TableLeases).
		IfNotExists().
		Define("subject", "VARCHAR(255)", "PRIMARY KEY").
		Define("lease_id", "VARCHAR(36)", "NOT NULL").
		Define("holder_uuid", "VARCHAR(36)", "NOT NULL").
		Define("holder_name", "VARCHAR(255)", "NOT NULL").
		Define("expires_at", "BIGINT", "NOT NULL")
	_, err := dbMan.Exec(ctx, dbs.MakeDbQuery(table.Build()))
	if err != nil {
		return nil, fmt.Errorf("failed to create table %s: %w", TableLeases, err)
	}
	return &Leases{dbMan: dbMan}, nil
}

// Acquire takes over an expired lease in the same upsert that inserts a missing one: a delete of the expired row first
// would lock the gap of a missing one on mysql, and concurrent acquires then deadlock on their inserts. It runs in one
// transaction so a call cut between its upsert and its read rolls back instead of leaving a lease nobody holds.
func (l *Leases) Acquire(ctx context.Context, subject string, holder LeaseHolder, ttl time.Duration) (Lease, error) {
	now := l.dbMan.NowMillisSQL()
	leaseId := uuid.NewString()

	ib := l.dbMan.Build().NewInsertBuilder()
	ib.InsertInto(TableLeases).
		Cols("subject", "lease_id", "holder_uuid", "holder_name", "expires_at").
		Values(subject, leaseId, holder.Uuid, holder.Name, sqlbuilder.Raw(expiresAt(now, ttl)))
	insertQuery, insertArgs := ib.Build()
	upsertQuery := takeOverExpired(l.dbMan.DbType, insertQuery, TableLeases, now, "lease_id", "holder_uuid", "holder_name")

	sb := l.dbMan.Build().NewSelectBuilder()
	sb.Select("lease_id", "holder_uuid", "holder_name", "expires_at").From(TableLeases).Where(sb.Equal("subject", subject))
	selectQuery, selectArgs := sb.Build()

	var currentId string
	var current LeaseHolder
	var expiresAtMillis int64
	err := l.dbMan.WithExecTx(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, upsertQuery, insertArgs...); err != nil {
			return err
		}
		row := tx.QueryRowContext(ctx, selectQuery, selectArgs...)
		return row.Scan(&currentId, &current.Uuid, &current.Name, &expiresAtMillis)
	})
	if err != nil {
		return Lease{}, fmt.Errorf("acquire lease %s: %w", subject, err)
	}
	lease := Lease{Holder: current, ExpiresAt: time.UnixMilli(expiresAtMillis)}
	if currentId == leaseId {
		lease.Granted = true
		lease.Id = leaseId
	}
	return lease, nil
}

// Renew answers ErrLeaseLost when the lease expired or was taken over, the holder must then stop what it did under it.
func (l *Leases) Renew(ctx context.Context, leaseId, holderName string, ttl time.Duration) (time.Time, error) {
	now := l.dbMan.NowMillisSQL()
	ub := l.dbMan.Build().NewUpdateBuilder()
	ub.Update(TableLeases).
		Set(ub.Assign("expires_at", sqlbuilder.Raw(expiresAt(now, ttl)))).
		Where(
			ub.Equal("lease_id", leaseId),
			ub.Equal("holder_name", holderName),
			ub.GreaterEqualThan("expires_at", sqlbuilder.Raw(now)),
		)
	updateQuery, updateArgs := ub.Build()

	sb := l.dbMan.Build().NewSelectBuilder()
	sb.Select("expires_at").From(TableLeases).Where(sb.Equal("lease_id", leaseId))
	selectQuery, selectArgs := sb.Build()

	var expiresAtMillis int64
	err := l.dbMan.WithExecTx(ctx, nil, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, updateQuery, updateArgs...)
		if err != nil {
			return err
		}
		renewed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if renewed == 0 {
			return ErrLeaseLost
		}
		return tx.QueryRowContext(ctx, selectQuery, selectArgs...).Scan(&expiresAtMillis)
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("renew lease %s: %w", leaseId, err)
	}
	return time.UnixMilli(expiresAtMillis), nil
}

// Release is idempotent: a lease already gone or held by another holder is not an error.
func (l *Leases) Release(ctx context.Context, leaseId, holderName string) error {
	db := l.dbMan.Build().NewDeleteBuilder()
	db.DeleteFrom(TableLeases).Where(db.Equal("lease_id", leaseId), db.Equal("holder_name", holderName))
	if _, err := l.dbMan.Exec(ctx, dbs.MakeDbQuery(db.Build())); err != nil {
		return fmt.Errorf("release lease %s: %w", leaseId, err)
	}
	return nil
}
