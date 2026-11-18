package ipams

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/huandu/go-sqlbuilder"
	"github.com/seagalia/flock/controller/dbs"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
)

// Tables
const (
	TableIPAMPool       = "ipam_pools"
	TableIPAMRange      = "ipam_ranges"
	TableIPAMAllocation = "ipam_allocations"
	TableIPAMRecycled   = "ipam_recycled"
)

// Indexes
const (
	IndexIPAMSourcesPool      = "idx_ipam_sources_pool"
	IndexIPAMRangesPriority   = "idx_ipam_ranges_pool_priority"
	IndexIPAMAllocationsRange = "idx_ipam_allocations_range"
	IndexIPAMAllocationsOwner = "idx_ipam_allocations_owner"
	IndexIPAMRecycledRange    = "idx_ipam_recycled_range"
)

type Db struct {
	dbMan *dbs.DbManager
}

func NewDb(ctx context.Context, dbMan *dbs.DbManager) (*Db, error) {
	dm := &Db{
		dbMan: dbMan,
	}
	err := dm.initTables(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to init tables: %w", err)
	}
	return dm, nil
}

func (s *Db) initTables(ctx context.Context) error {
	addrType := s.binaryType()

	pools := s.newTable(TableIPAMPool).
		Define("id", "VARCHAR(36)", "PRIMARY KEY").
		Define("zone_uuid", "VARCHAR(36)", "NOT NULL", "UNIQUE").
		Define("created_at", "BIGINT", "NOT NULL").
		Define("updated_at", "BIGINT", "NOT NULL")

	ranges := s.newTable(TableIPAMRange).
		Define("id", "VARCHAR(36)", "PRIMARY KEY").
		Define("pool_id", "VARCHAR(36)", "NOT NULL").
		Define("pool_name", "VARCHAR(255)", "NOT NULL", "DEFAULT ''").
		Define("family", "SMALLINT", "NOT NULL").
		Define("start_addr", addrType, "NOT NULL").
		Define("end_addr", addrType, "NOT NULL").
		Define("cursor_addr", addrType).
		Define("priority", "INTEGER", "NOT NULL", "DEFAULT 0").
		Define("created_at", "BIGINT", "NOT NULL").
		Define(fmt.Sprintf(
			"FOREIGN KEY (pool_id) REFERENCES %s(id)",
			TableIPAMPool,
		)).
		Define("UNIQUE (pool_id, id)").
		Define("CHECK (family IN (4, 6))")

	allocations := s.newTable(TableIPAMAllocation).
		Define("id", "VARCHAR(36)", "PRIMARY KEY").
		Define("pool_id", "VARCHAR(36)", "NOT NULL").
		Define("range_id", "VARCHAR(36)", "NOT NULL").
		Define("family", "SMALLINT", "NOT NULL").
		Define("ip", addrType, "NOT NULL").
		Define("owner", "VARCHAR(255)", "NOT NULL").
		Define("created_at", "BIGINT", "NOT NULL").
		Define(fmt.Sprintf(
			"FOREIGN KEY (pool_id, range_id) REFERENCES %s(pool_id, id)",
			TableIPAMRange,
		)).
		Define("UNIQUE (pool_id, family, ip)").
		Define("CHECK (family IN (4, 6))")

	recycled := s.newTable(TableIPAMRecycled).
		Define("pool_id", "VARCHAR(36)", "NOT NULL").
		Define("range_id", "VARCHAR(36)", "NOT NULL").
		Define("family", "SMALLINT", "NOT NULL").
		Define("ip", addrType, "NOT NULL").
		Define("released_at", "BIGINT", "NOT NULL").
		Define("PRIMARY KEY (range_id, family, ip)").
		Define(fmt.Sprintf(
			"FOREIGN KEY (pool_id, range_id) REFERENCES %s(pool_id, id)",
			TableIPAMRange,
		)).
		Define("CHECK (family IN (4, 6))")

	indexes := []struct {
		name      string
		table     *sqlbuilder.CreateTableBuilder
		tableName string
		columns   string
	}{
		{IndexIPAMRangesPriority, ranges, TableIPAMRange, "pool_id, priority, id"},
		{IndexIPAMAllocationsRange, allocations, TableIPAMAllocation, "range_id, family, ip"},
		{IndexIPAMAllocationsOwner, allocations, TableIPAMAllocation, "owner"},
		{IndexIPAMRecycledRange, recycled, TableIPAMRecycled, "pool_id, range_id, released_at"},
	}
	// tables are created at each start: MySQL has no CREATE INDEX IF NOT EXISTS, its indexes are
	// declared in CREATE TABLE IF NOT EXISTS instead
	var indexQueries []dbs.DbQuery
	for _, index := range indexes {
		if s.dbMan.DbType == dbs.DbTypeMysql {
			index.table.Define(fmt.Sprintf("INDEX %s (%s)", index.name, index.columns))
			continue
		}
		indexQueries = append(indexQueries, dbs.MakeDbQuery(fmt.Sprintf(
			"CREATE INDEX IF NOT EXISTS %s ON %s (%s)", index.name, index.tableName, index.columns,
		), nil))
	}

	queries := slices.Concat([]dbs.DbQuery{
		dbs.MakeDbQuery(pools.Build()),
		dbs.MakeDbQuery(ranges.Build()),
		dbs.MakeDbQuery(allocations.Build()),
		dbs.MakeDbQuery(recycled.Build()),
	}, indexQueries)

	_, err := s.dbMan.ExecTx(ctx, queries)
	if err != nil {
		return fmt.Errorf("failed to exec query: %w", err)
	}
	return nil
}

func (s *Db) binaryType() string {
	switch s.dbMan.DbType {
	case dbs.DbTypeSqlite:
		return "BLOB"
	case dbs.DbTypePostgres:
		return "BYTEA"
	case dbs.DbTypeMysql:
		return "VARBINARY(16)"
	default:
		panic("unsupported database backend")
	}
}

func (s *Db) newTable(name string) *sqlbuilder.CreateTableBuilder {
	return s.dbMan.Build().NewCreateTableBuilder().
		CreateTable(name).
		IfNotExists()
}

// lockZonePool creates the pool row of the zone when missing, then locks it: the ranges reference it, and
// the lock serializes the syncs and the acquisitions of the zone.
func (s *Db) lockZonePool(ctx context.Context, tx *sql.Tx, zoneUUID string) error {
	now := time.Now().UnixMilli()

	// INSERT-first strategy but without doing anything if it does already exists.
	ib := s.dbMan.Build().NewInsertBuilder()
	ib.InsertInto(TableIPAMPool).
		Cols("id", "zone_uuid", "created_at", "updated_at").
		Values(zoneUUID, zoneUUID, now, now)

	query, args := ib.Build()

	switch s.dbMan.DbType {
	case dbs.DbTypePostgres, dbs.DbTypeSqlite:
		query += " ON CONFLICT DO NOTHING"
	case dbs.DbTypeMysql:
		query += " ON DUPLICATE KEY UPDATE id = id"
	}

	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return err
	}
	return s.lockPool(ctx, tx, zoneUUID)
}

// lockPool serializes a release with the acquisitions, which move the cursor of the ranges of the pool.
// Sqlite needs nothing: its write transactions are immediate.
func (s *Db) lockPool(ctx context.Context, tx *sql.Tx, poolID string) error {
	if s.dbMan.DbType == dbs.DbTypeSqlite {
		return nil
	}
	sb := s.dbMan.Build().NewSelectBuilder()
	sb.Select("id").From(TableIPAMPool).Where(sb.Equal("id", poolID)).ForUpdate()
	query, args := sb.Build()
	var id string
	err := tx.QueryRowContext(ctx, query, args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrIPNotAllocated
	}
	return err
}

// prepareRanges locks the pool of the zone for an acquisition. Acquisitions trust the stored ranges and
// sync only a zone with none stored (created while the ipam was off): syncing on each acquisition would let
// one reading an older zone bring back a range an update removed.
func (s *Db) prepareRanges(ctx context.Context, tx *sql.Tx, aZone *zone.Zone) error {
	err := s.lockZonePool(ctx, tx, aZone.GetUUID())
	if err != nil {
		return err
	}
	ranges, err := s.loadRanges(ctx, tx, aZone.GetUUID())
	if err != nil {
		return err
	}
	if len(ranges) > 0 {
		return nil
	}
	return s.syncRanges(ctx, tx, aZone)
}

func (s *Db) SyncZone(ctx context.Context, aZone *zone.Zone) error {
	opts := &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	}
	return s.dbMan.WithExecTx(ctx, opts, func(tx *sql.Tx) error {
		err := s.lockZonePool(ctx, tx, aZone.GetUUID())
		if err != nil {
			return err
		}
		return s.syncRanges(ctx, tx, aZone)
	})
}

type rangeBounds struct {
	start netip.Addr
	end   netip.Addr
}

// syncRanges makes the stored ranges of the zone match its intervals, the pool row being locked. A stored
// range is matched on its bounds only, so renaming a pool retags its ranges and keeps their allocations.
// Changing the bounds of a range is a delete plus an insert: refused when the range holds an allocation,
// the transaction of the caller then rolls everything back.
func (s *Db) syncRanges(ctx context.Context, tx *sql.Tx, aZone *zone.Zone) error {
	err := ValidateZone(aZone)
	if err != nil {
		return err
	}
	intervals, err := intervalsFromZone(aZone)
	if err != nil {
		return err
	}
	stored, err := s.loadRanges(ctx, tx, aZone.GetUUID())
	if err != nil {
		return err
	}

	storedByBounds := make(map[rangeBounds]IPRange, len(stored))
	for _, r := range stored {
		storedByBounds[rangeBounds{start: r.Start, end: r.End}] = r
	}

	declared := make(map[rangeBounds]bool, len(intervals))
	for priority, interval := range intervals {
		bounds := rangeBounds{start: interval.Start, end: interval.End}
		declared[bounds] = true

		existing, found := storedByBounds[bounds]
		if !found {
			err := s.insertRange(ctx, tx, aZone.GetUUID(), interval, priority)
			if err != nil {
				return err
			}
			continue
		}
		if existing.PoolName == interval.Pool {
			continue
		}
		err := s.renameRange(ctx, tx, existing.ID, interval.Pool)
		if err != nil {
			return err
		}
	}

	for _, r := range stored {
		if declared[rangeBounds{start: r.Start, end: r.End}] {
			continue
		}
		err := s.deleteFreeRange(ctx, tx, r)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Db) insertRange(ctx context.Context, tx *sql.Tx, poolID string, interval IPInterval, priority int) error {
	ib := s.dbMan.Build().NewInsertBuilder()
	ib.InsertInto(TableIPAMRange).
		Cols(
			"id", "pool_id", "pool_name", "family",
			"start_addr", "end_addr",
			"cursor_addr", "priority", "created_at",
		).
		Values(
			uuid.NewString(),
			poolID,
			interval.Pool,
			ipFamily(interval.Start),
			encodeIP(interval.Start),
			encodeIP(interval.End),
			encodeIP(interval.Start),
			priority,
			time.Now().UnixMilli(),
		)

	query, args := ib.Build()
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func (s *Db) renameRange(ctx context.Context, tx *sql.Tx, rangeID, poolName string) error {
	ub := s.dbMan.Build().NewUpdateBuilder()
	ub.Update(TableIPAMRange).
		Set(ub.Assign("pool_name", poolName)).
		Where(ub.Equal("id", rangeID))
	query, args := ub.Build()
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

// deleteFreeRange deletes a range its zone no longer declares, with its recycled ips, unless one of its
// ips is allocated.
func (s *Db) deleteFreeRange(ctx context.Context, tx *sql.Tx, r IPRange) error {
	sb := s.dbMan.Build().NewSelectBuilder()
	sb.Select("id").From(TableIPAMAllocation).Where(sb.Equal("range_id", r.ID)).Limit(1)
	query, args := sb.Build()
	var allocationID string
	err := tx.QueryRowContext(ctx, query, args...).Scan(&allocationID)
	if err == nil {
		return fmt.Errorf("range %s - %s of pool %q holds allocated IPs and can not be removed or resized: %w", r.Start, r.End, r.PoolName, ErrIPsOutOfRanges)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	deleteRecycled := s.dbMan.Build().NewDeleteBuilder()
	deleteRecycled.DeleteFrom(TableIPAMRecycled).Where(deleteRecycled.Equal("range_id", r.ID))
	deleteRange := s.dbMan.Build().NewDeleteBuilder()
	deleteRange.DeleteFrom(TableIPAMRange).Where(deleteRange.Equal("id", r.ID))
	for _, builder := range []*sqlbuilder.DeleteBuilder{deleteRecycled, deleteRange} {
		query, args := builder.Build()
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	return nil
}

// allocationOwner gives the owner of ip in the pool, found is false when ip is not allocated.
func (s *Db) allocationOwner(
	ctx context.Context,
	tx *sql.Tx,
	poolID string,
	ip netip.Addr,
) (owner string, found bool, err error) {
	sb := s.dbMan.Build().NewSelectBuilder()
	sb.Select("owner").
		From(TableIPAMAllocation).
		Where(
			sb.Equal("pool_id", poolID),
			sb.Equal("family", ipFamily(ip)),
			sb.Equal("ip", encodeIP(ip)),
		).
		Limit(1)

	query, args := sb.Build()

	err = tx.QueryRowContext(ctx, query, args...).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return owner, true, nil
}

func (s *Db) insertAllocation(
	ctx context.Context,
	tx *sql.Tx,
	poolID, rangeID, owner string,
	ip netip.Addr,
) error {
	ib := s.dbMan.Build().NewInsertBuilder()
	ib.InsertInto(TableIPAMAllocation).
		Cols(
			"id", "pool_id", "range_id",
			"family", "ip", "owner", "created_at",
		).
		Values(
			uuid.NewString(), poolID, rangeID,
			ipFamily(ip), encodeIP(ip),
			owner, time.Now().UnixMilli(),
		)

	query, args := ib.Build()
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func (s *Db) updateCursor(
	ctx context.Context,
	tx *sql.Tx,
	rangeID string,
	next netip.Addr,
) error {
	ub := s.dbMan.Build().NewUpdateBuilder()
	ub.Update(TableIPAMRange)

	if next.IsValid() {
		ub.Set(ub.Assign("cursor_addr", encodeIP(next)))
	} else {
		ub.Set(ub.Assign("cursor_addr", nil))
	}

	ub.Where(ub.Equal("id", rangeID))

	query, args := ub.Build()
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func (s *Db) deleteRecycled(
	ctx context.Context,
	tx *sql.Tx,
	rangeID string,
	ip netip.Addr,
) error {
	db := s.dbMan.Build().NewDeleteBuilder()
	db.DeleteFrom(TableIPAMRecycled).
		Where(
			db.Equal("range_id", rangeID),
			db.Equal("family", ipFamily(ip)),
			db.Equal("ip", encodeIP(ip)),
		)

	query, args := db.Build()
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func (s *Db) popRecycled(
	ctx context.Context,
	tx *sql.Tx,
	rangeID string,
) (netip.Addr, error) {
	sb := s.dbMan.Build().NewSelectBuilder()
	sb.Select("ip").
		From(TableIPAMRecycled).
		Where(sb.Equal("range_id", rangeID)).
		OrderByAsc("released_at").
		OrderByAsc("ip").
		Limit(1)

	query, args := sb.Build()

	var data []byte
	err := tx.QueryRowContext(
		ctx, query, args...,
	).Scan(&data)

	if err != nil {
		return netip.Addr{}, err
	}

	ip, err := decodeIP(data)
	if err != nil {
		return netip.Addr{}, err
	}

	if err := s.deleteRecycled(
		ctx, tx, rangeID, ip,
	); err != nil {
		return netip.Addr{}, err
	}

	return ip, nil
}

func (s *Db) loadRanges(
	ctx context.Context,
	tx *sql.Tx,
	poolID string,
) ([]IPRange, error) {
	sb := s.dbMan.Build().NewSelectBuilder()
	sb.Select(
		"id", "pool_id", "pool_name", "family",
		"start_addr", "end_addr",
		"cursor_addr", "priority",
	).
		From(TableIPAMRange).
		Where(sb.Equal("pool_id", poolID)).
		OrderByAsc("priority").
		OrderByAsc("id")

	query, args := sb.Build()
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ranges []IPRange

	for rows.Next() {
		var r IPRange
		var start, end, cursor []byte

		err := rows.Scan(
			&r.ID, &r.PoolID, &r.PoolName, &r.Family,
			&start, &end, &cursor, &r.Priority,
		)
		if err != nil {
			return nil, err
		}

		r.Start, err = decodeIP(start)
		if err != nil {
			return nil, err
		}
		r.End, err = decodeIP(end)
		if err != nil {
			return nil, err
		}

		if cursor != nil {
			r.Cursor, err = decodeIP(cursor)
			if err != nil {
				return nil, err
			}
		}

		ranges = append(ranges, r)
	}

	return ranges, rows.Err()
}

func rangesOfPool(ranges []IPRange, pool string) []IPRange {
	var ofPool []IPRange
	for _, r := range ranges {
		if r.PoolName == pool {
			ofPool = append(ofPool, r)
		}
	}
	return ofPool
}

func (s *Db) AcquireIP(
	ctx context.Context,
	aZone *zone.Zone,
	pool string,
	owner string,
) (netip.Addr, error) {
	if aZone.CouldNotAllocateIP() {
		return netip.Addr{}, fmt.Errorf("zone %s: %w", aZone.GetUUID(), ErrNoAllocatableIP)
	}
	if aZone.FindPool(pool, nil) == nil {
		return netip.Addr{}, fmt.Errorf("pool %q of zone %s: %w", pool, aZone.GetUUID(), ErrPoolNotFound)
	}
	var acquired netip.Addr
	opts := &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	}
	err := s.dbMan.WithExecTx(ctx, opts, func(tx *sql.Tx) error {
		poolID := aZone.GetUUID()
		err := s.prepareRanges(ctx, tx, aZone)
		if err != nil {
			return err
		}

		ranges, err := s.loadRanges(ctx, tx, poolID)
		if err != nil {
			return err
		}
		ranges = rangesOfPool(ranges, pool)
		if len(ranges) == 0 {
			return fmt.Errorf("pool %q of zone %s has no stored range: %w", pool, poolID, ErrPoolNotFound)
		}

		for _, r := range ranges {
			ip, err := s.popRecycled(ctx, tx, r.ID)

			if err == nil {
				if !containsIP(r, ip) {
					return fmt.Errorf(
						"recycled IP %s is outside its range",
						ip,
					)
				}

				if err := s.insertAllocation(
					ctx, tx, poolID, r.ID, owner, ip,
				); err != nil {
					return err
				}

				acquired = ip
				return nil
			}

			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}

			if !r.Cursor.IsValid() {
				continue
			}

			for ip := r.Cursor; ip.IsValid(); ip = nextIP(ip, r.End) {
				_, allocated, err := s.allocationOwner(
					ctx, tx, poolID, ip,
				)
				if err != nil {
					return err
				}
				if allocated {
					continue
				}

				if err := s.updateCursor(
					ctx, tx, r.ID, nextIP(ip, r.End),
				); err != nil {
					return err
				}

				if err := s.insertAllocation(
					ctx, tx, poolID, r.ID, owner, ip,
				); err != nil {
					return err
				}

				acquired = ip
				return nil
			}

			if err := s.updateCursor(
				ctx, tx, r.ID, netip.Addr{},
			); err != nil {
				return err
			}
		}

		return ErrPoolExhausted
	})

	if err != nil {
		return netip.Addr{}, err
	}

	return acquired, nil
}

func (s *Db) AcquireSpecificIP(
	ctx context.Context,
	aZone *zone.Zone,
	ip netip.Addr,
	owner string,
) error {
	if aZone.CouldNotAllocateIP() {
		return nil
	}
	if !ip.IsValid() {
		return errors.New("invalid IP")
	}
	ip = ip.Unmap()

	opts := &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	}
	return s.dbMan.WithExecTx(ctx, opts, func(tx *sql.Tx) error {
		poolID := aZone.GetUUID()
		err := s.prepareRanges(ctx, tx, aZone)
		if err != nil {
			return err
		}

		ranges, err := s.loadRanges(ctx, tx, poolID)
		if err != nil {
			return err
		}

		for _, r := range ranges {
			if !containsIP(r, ip) {
				continue
			}

			currentOwner, allocated, err := s.allocationOwner(
				ctx, tx, poolID, ip,
			)
			if err != nil {
				return err
			}
			// a vip attached again in the zone it already has its ip in
			if allocated && currentOwner == owner {
				return nil
			}
			if allocated {
				return ErrIPAllocated
			}

			if err := s.deleteRecycled(
				ctx, tx, r.ID, ip,
			); err != nil {
				return err
			}

			return s.insertAllocation(
				ctx, tx, poolID, r.ID, owner, ip,
			)
		}

		return ErrIPOutOfRange
	})
}

// ReleaseIP does not check the zone configuration like the acquisitions: an ip must still be
// releasable after its zone changed.
func (s *Db) ReleaseIP(
	ctx context.Context,
	aZone *zone.Zone,
	ip netip.Addr,
) error {
	if aZone.CouldNotAllocateIP() {
		return nil
	}
	if !ip.IsValid() {
		return errors.New("invalid IP")
	}
	opts := &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	}
	return s.dbMan.WithExecTx(ctx, opts, func(tx *sql.Tx) error {
		return s.releaseAllocation(ctx, tx, aZone.GetUUID(), ip.Unmap())
	})
}

func (s *Db) ReleaseOwner(ctx context.Context, owner string) error {
	opts := &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	}
	return s.dbMan.WithExecTx(ctx, opts, func(tx *sql.Tx) error {
		sb := s.dbMan.Build().NewSelectBuilder()
		sb.Select("pool_id", "ip").
			From(TableIPAMAllocation).
			Where(sb.Equal("owner", owner))
		query, args := sb.Build()
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		type allocation struct {
			poolID string
			ip     netip.Addr
		}
		var allocations []allocation
		for rows.Next() {
			var poolID string
			var data []byte
			if err := rows.Scan(&poolID, &data); err != nil {
				rows.Close()
				return err
			}
			ip, err := decodeIP(data)
			if err != nil {
				rows.Close()
				return err
			}
			allocations = append(allocations, allocation{poolID: poolID, ip: ip})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, alloc := range allocations {
			if err := s.releaseAllocation(ctx, tx, alloc.poolID, alloc.ip); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Db) releaseAllocation(
	ctx context.Context,
	tx *sql.Tx,
	poolID string,
	ip netip.Addr,
) error {
	err := s.lockPool(ctx, tx, poolID)
	if err != nil {
		return err
	}

	sb := s.dbMan.Build().NewSelectBuilder()
	sb.Select("id", "range_id").
		From(TableIPAMAllocation).
		Where(
			sb.Equal("pool_id", poolID),
			sb.Equal("family", ipFamily(ip)),
			sb.Equal("ip", encodeIP(ip)),
		)

	query, args := sb.Build()

	var allocationID, rangeID string
	err = tx.QueryRowContext(
		ctx, query, args...,
	).Scan(&allocationID, &rangeID)

	if errors.Is(err, sql.ErrNoRows) {
		return ErrIPNotAllocated
	}
	if err != nil {
		return err
	}

	ranges, err := s.loadRanges(ctx, tx, poolID)
	if err != nil {
		return err
	}

	var target *IPRange
	for i := range ranges {
		if ranges[i].ID == rangeID {
			target = &ranges[i]
			break
		}
	}

	if target == nil {
		return errors.New("allocation range not found")
	}

	db := s.dbMan.Build().NewDeleteBuilder()
	db.DeleteFrom(TableIPAMAllocation).
		Where(db.Equal("id", allocationID))

	query, args = db.Build()
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrIPNotAllocated
	}

	// The cursor will encounter this address later.
	if target.Cursor.IsValid() &&
		ip.Compare(target.Cursor) >= 0 {
		return nil
	}

	ib := s.dbMan.Build().NewInsertBuilder()
	ib.InsertInto(TableIPAMRecycled).
		Cols(
			"pool_id", "range_id",
			"family", "ip", "released_at",
		).
		Values(
			poolID, rangeID, ipFamily(ip),
			encodeIP(ip), time.Now().UnixMilli(),
		)

	query, args = ib.Build()
	_, err = tx.ExecContext(ctx, query, args...)
	return err
}

func (s *Db) HasAllocatedIPs(
	ctx context.Context,
	aZone *zone.Zone,
) (bool, error) {
	if aZone.CouldNotAllocateIP() {
		return false, nil
	}
	sb := s.dbMan.Build().NewSelectBuilder()

	sb.Select("1").
		From(TableIPAMAllocation).
		Where(sb.Equal("pool_id", aZone.GetUUID())).
		Limit(1)

	rows, err := s.dbMan.Query(ctx, dbs.MakeDbQuery(sb.Build()))
	if err != nil {
		return false, fmt.Errorf(
			"check allocated IPs for zone %s: %w",
			aZone.GetUUID(), err,
		)
	}
	defer rows.Close()

	if !rows.Next() {
		return false, nil
	}

	var exists int
	err = rows.Scan(&exists)
	if err != nil {
		return false, fmt.Errorf(
			"check allocated IPs for zone %s: %w",
			aZone.GetUUID(), err,
		)
	}

	return true, nil
}
