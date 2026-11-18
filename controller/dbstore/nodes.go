package dbstore

import (
	"context"
	"database/sql"
	goerrors "errors"
	"fmt"
	"strconv"
	"time"

	"github.com/huandu/go-sqlbuilder"
	"github.com/seagalia/flock/controller/dbs"
	"github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	"github.com/seagalia/flock/sdk/storers"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type nodeStore struct {
	dbMan *dbs.DbManager
}

// ClaimNode takes over the row of another address in the same upsert that inserts a missing one, then reads back the
// address holding the row. A delete of the row to take over first would lock the gap of a missing row on mysql, and
// the concurrent claims of the streams of one agent then deadlock on their inserts. The transaction keeps a claim cut
// between its upsert and its read from leaving a fresh row that refuses the other addresses until it is stale.
func (n *nodeStore) ClaimNode(
	ctx context.Context,
	instanceUuid string,
	node *svclookup.InstanceNode,
	addr string,
	staleAfter time.Duration,
) error {
	data, err := protojson.Marshal(node.GetNode())
	if err != nil {
		return fmt.Errorf("failed to marshal node: %w", err)
	}
	agentUuid := node.GetAgentUuid()
	now := n.dbMan.NowMillisSQL()
	staleBefore := now + " - " + strconv.FormatInt(staleAfter.Milliseconds(), 10)

	ins := n.dbMan.Build().NewInsertBuilder()
	ins.InsertInto(nodesTableName).
		Cols("instance_uuid", "agent_uuid", "name", "addr", "data", "last_seen").
		Values(instanceUuid, agentUuid, node.GetName(), addr, string(data), sqlbuilder.Raw(now))
	insQuery, insArgs := ins.Build()
	// qualified, because the existing row and the inserted one (excluded on postgres and sqlite, new on mysql) share
	// the column names
	takeoverBy := func(inserted string) string {
		return nodesTableName + ".addr = " + inserted + ".addr OR " + nodesTableName + ".last_seen < " + staleBefore
	}
	switch n.dbMan.DbType {
	case dbs.DbTypePostgres, dbs.DbTypeSqlite:
		insQuery += " ON CONFLICT (instance_uuid, agent_uuid) DO UPDATE SET" +
			" name = excluded.name, addr = excluded.addr, data = excluded.data, last_seen = excluded.last_seen" +
			" WHERE " + takeoverBy("excluded")
	case dbs.DbTypeMysql:
		// mysql runs the assignments left to right, each seeing the values assigned before it: addr goes before
		// last_seen, so a taken over row has the new addr when last_seen checks the condition
		takeover := takeoverBy("new")
		insQuery += " AS new ON DUPLICATE KEY UPDATE" +
			fmt.Sprintf(" name = IF(%s, new.name, %s.name),", takeover, nodesTableName) +
			fmt.Sprintf(" data = IF(%s, new.data, %s.data),", takeover, nodesTableName) +
			fmt.Sprintf(" addr = IF(%s, new.addr, %s.addr),", takeover, nodesTableName) +
			fmt.Sprintf(" last_seen = IF(%s, new.last_seen, %s.last_seen)", takeover, nodesTableName)
	}

	sel := n.dbMan.Build().NewSelectBuilder()
	sel.Select("addr").From(nodesTableName).Where(
		sel.Equal("instance_uuid", instanceUuid),
		sel.Equal("agent_uuid", agentUuid),
	)
	selQuery, selArgs := sel.Build()

	var holderAddr string
	err = n.dbMan.WithExecTx(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, insQuery, insArgs...); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, selQuery, selArgs...).Scan(&holderAddr)
	})
	if err != nil {
		return fmt.Errorf("failed to claim node: %w", err)
	}
	if holderAddr != addr {
		return &storers.NodeHeldError{Addr: holderAddr}
	}
	return nil
}

func (n *nodeStore) TouchNode(ctx context.Context, instanceUuid, agentUuid, addr string) error {
	ub := n.dbMan.Build().NewUpdateBuilder()
	ub.Update(nodesTableName).
		Set(ub.Assign("last_seen", sqlbuilder.Raw(n.dbMan.NowMillisSQL()))).
		Where(ub.Equal("instance_uuid", instanceUuid), ub.Equal("agent_uuid", agentUuid), ub.Equal("addr", addr))
	_, err := n.dbMan.Exec(ctx, dbs.MakeDbQuery(ub.Build()))
	if err != nil {
		return fmt.Errorf("failed to touch node: %w", err)
	}
	return nil
}

func (n *nodeStore) ListNodes(ctx context.Context, instanceUuid string) ([]*svclookup.InstanceNode, error) {
	qb := n.dbMan.Build().NewSelectBuilder()
	qb.Select("agent_uuid", "name", "data", "last_seen").From(nodesTableName).
		Where(qb.Equal("instance_uuid", instanceUuid)).
		OrderBy("name")
	rows, err := n.dbMan.Query(ctx, dbs.MakeDbQuery(qb.Build()))
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}
	defer rows.Close()

	result := []*svclookup.InstanceNode{}
	for rows.Next() {
		var agentUuid, name, data string
		var lastSeenMillis int64
		if err := rows.Scan(&agentUuid, &name, &data, &lastSeenMillis); err != nil {
			return nil, fmt.Errorf("failed to scan node: %w", err)
		}
		node := &core.Node{}
		if err := protojson.Unmarshal([]byte(data), node); err != nil {
			return nil, fmt.Errorf("failed to unmarshal node: %w", err)
		}
		result = append(result, &svclookup.InstanceNode{
			Node:      node,
			AgentUuid: agentUuid,
			Name:      name,
			LastSeen:  timestamppb.New(time.UnixMilli(lastSeenMillis)),
		})
	}
	return result, rows.Err()
}

func (n *nodeStore) FindNodeInstance(ctx context.Context, agentUuid string) (string, error) {
	qb := n.dbMan.Build().NewSelectBuilder()
	qb.Select("instance_uuid").From(nodesTableName).
		Where(qb.Equal("agent_uuid", agentUuid)).
		OrderByDesc("last_seen").
		Limit(1)
	query, args := qb.Build()
	var instanceUuid string
	err := n.dbMan.QueryRow(ctx, dbs.MakeDbQuery(query, args)).Scan(&instanceUuid)
	if goerrors.Is(err, sql.ErrNoRows) {
		return "", errors.NewErrNotFound("node", agentUuid)
	}
	if err != nil {
		return "", fmt.Errorf("failed to find the instance of the node: %w", err)
	}
	return instanceUuid, nil
}

func (n *nodeStore) DeleteNodes(ctx context.Context, instanceUuid string) error {
	del := n.dbMan.Build().NewDeleteBuilder()
	del.DeleteFrom(nodesTableName).Where(del.Equal("instance_uuid", instanceUuid))
	_, err := n.dbMan.Exec(ctx, dbs.MakeDbQuery(del.Build()))
	if err != nil {
		return fmt.Errorf("failed to delete nodes: %w", err)
	}
	return nil
}
