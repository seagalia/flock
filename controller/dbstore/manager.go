package dbstore

import (
	"context"
	"fmt"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/controller/dbs"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	secretcfg "github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/logging"
	"github.com/seagalia/flock/sdk/storers"
	_ "modernc.org/sqlite"
)

const (
	foreignResourcesTableName = "ForeignResources"
	permTableName             = "Permissions"
	metaTableName             = "Metadata"
	nodesTableName            = "AgentNodes"
	oldNodesTableName         = "InstanceNodes"
)

type LightStorer interface {
	LightList(ctx context.Context) (map[string]string, error)
}

type DbStoreManager struct {
	entry          zerolog.Logger
	secret         string
	dbMan          *dbs.DbManager
	backends       storers.Storer[*backend.Backend]
	instances      storers.Storer[*instance.Instance]
	vips           storers.Storer[*vip.VIP]
	virtualServers storers.Storer[*vs.VirtualServer]
	certificates   storers.Storer[*cert.Certificate]
	secrets        storers.Storer[*secretcfg.Secret]
	zones          storers.Storer[*zone.Zone]
	nodes          *nodeStore
}

func NewDbStoreManager(
	ctx context.Context,
	dbMan *dbs.DbManager,
	secret string,
) (*DbStoreManager, error) {
	dsm := &DbStoreManager{
		entry:  logging.Component("dbstore"),
		dbMan:  dbMan,
		secret: secret,
	}
	transHook, err := NewEncryptTransHook(dsm.secret)
	if err != nil {
		return nil, err
	}

	err = dsm.initTables(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to init foreign table: %s", err.Error())
	}

	dsm.backends = NewDbStore[*backend.Backend](ctx, dbMan, nil)
	dsm.instances = NewDbStore[*instance.Instance](ctx, dbMan, nil)
	dsm.vips = NewDbStore[*vip.VIP](ctx, dbMan, nil)
	dsm.virtualServers = NewDbStore[*vs.VirtualServer](ctx, dbMan, nil)
	dsm.certificates = NewDbStore[*cert.Certificate](ctx, dbMan, transHook)
	dsm.secrets = NewDbStore[*secretcfg.Secret](ctx, dbMan, transHook)
	dsm.zones = NewDbStore[*zone.Zone](ctx, dbMan, nil)
	dsm.nodes = &nodeStore{dbMan: dbMan}

	storeCollector := NewStoreInfoCollector()
	prometheus.MustRegister(storeCollector)
	storeCollector.AddStore(helpers.TypeUrl[*backend.Backend](), dsm.backends.(LightStorer))
	storeCollector.AddStore(helpers.TypeUrl[*instance.Instance](), dsm.instances.(LightStorer))
	storeCollector.AddStore(helpers.TypeUrl[*vip.VIP](), dsm.vips.(LightStorer))
	storeCollector.AddStore(helpers.TypeUrl[*vs.VirtualServer](), dsm.virtualServers.(LightStorer))
	storeCollector.AddStore(helpers.TypeUrl[*zone.Zone](), dsm.zones.(LightStorer))
	storeCollector.AddStore(helpers.TypeUrl[*secretcfg.Secret](), dsm.secrets.(LightStorer))

	prometheus.MustRegister(NewForeignStoreInfoCollector(dbMan))
	return dsm, nil
}

func (d *DbStoreManager) initTables(ctx context.Context) error {
	ctbForeign := d.dbMan.Build().NewCreateTableBuilder()
	ctbForeign.
		CreateTable(foreignResourcesTableName).
		IfNotExists().
		Define("id", "CHAR(20)", "NOT NULL", "PRIMARY KEY").
		Define("source_type", "VARCHAR(255)", "NOT NULL").
		Define("source_uuid", "VARCHAR(255)", "NOT NULL").
		Define("foreign_type", "VARCHAR(255)", "NOT NULL").
		Define("foreign_uuid", "VARCHAR(255)", "NOT NULL")
	ctbPerm := d.dbMan.Build().NewCreateTableBuilder()
	ctbPerm.
		CreateTable(permTableName).
		IfNotExists().
		Define("id", "CHAR(20)", "NOT NULL", "PRIMARY KEY").
		Define("resource_type", "VARCHAR(255)", "NOT NULL").
		Define("resource_uuid", "VARCHAR(255)", "NOT NULL").
		Define("role", "INTEGER", "NOT NULL").
		Define("member_type", "INTEGER", "NOT NULL").
		Define("member_name", "VARCHAR(255)", "NOT NULL")

	ctbMeta := d.dbMan.Build().NewCreateTableBuilder()
	ctbMeta.
		CreateTable(metaTableName).
		IfNotExists().
		Define("id", "CHAR(20)", "NOT NULL", "PRIMARY KEY").
		Define("resource_type", "VARCHAR(255)", "NOT NULL").
		Define("resource_uuid", "VARCHAR(255)", "NOT NULL").
		Define("name", "VARCHAR(255)", "NOT NULL").
		Define("value", "VARCHAR(255)", "NOT NULL")

	ctbNodes := d.dbMan.Build().NewCreateTableBuilder()
	ctbNodes.
		CreateTable(nodesTableName).
		IfNotExists().
		Define("instance_uuid", "VARCHAR(255)", "NOT NULL").
		Define("agent_uuid", "VARCHAR(255)", "NOT NULL").
		Define("name", "VARCHAR(255)", "NOT NULL").
		Define("addr", "VARCHAR(255)", "NOT NULL").
		Define("data", "TEXT", "NOT NULL").
		Define("last_seen", "BIGINT", "NOT NULL").
		Define("PRIMARY KEY (instance_uuid, agent_uuid)")

	queries := []dbs.DbQuery{
		dbs.MakeDbQuery(ctbForeign.Build()),
		dbs.MakeDbQuery(ctbPerm.Build()),
		dbs.MakeDbQuery(ctbMeta.Build()),
		dbs.MakeDbQuery("DROP TABLE IF EXISTS "+oldNodesTableName, nil),
		dbs.MakeDbQuery(ctbNodes.Build()),
	}
	_, err := d.dbMan.ExecTx(ctx, queries)
	if err != nil {
		return fmt.Errorf("failed to exec query: %w", err)
	}
	return nil
}

func (d *DbStoreManager) Backends() storers.Storer[*backend.Backend] {
	return d.backends
}

func (d *DbStoreManager) Instances() storers.Storer[*instance.Instance] {
	return d.instances
}

func (d *DbStoreManager) VIPs() storers.Storer[*vip.VIP] {
	return d.vips
}

func (d *DbStoreManager) VirtualServers() storers.Storer[*vs.VirtualServer] {
	return d.virtualServers
}

func (d *DbStoreManager) Certificates() storers.Storer[*cert.Certificate] {
	return d.certificates
}

func (d *DbStoreManager) Nodes() storers.NodeStore {
	return d.nodes
}

func (d *DbStoreManager) Zones() storers.Storer[*zone.Zone] {
	return d.zones
}

func (d *DbStoreManager) Secrets() storers.Storer[*secretcfg.Secret] {
	return d.secrets
}
