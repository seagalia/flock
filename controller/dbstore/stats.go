package dbstore

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
	"github.com/seagalia/flock/controller/dbs"
)

const (
	collectTimeout = 30 * time.Second
)

type StoreInfoCollector struct {
	desc   *prometheus.Desc
	stores map[string]LightStorer
}

func NewStoreInfoCollector() *StoreInfoCollector {
	return &StoreInfoCollector{
		stores: make(map[string]LightStorer, 0),
		desc: prometheus.NewDesc(
			"flock_controller_store_info",
			"Information about resource in the store",
			[]string{"type_url", "uuid", "name"},
			nil,
		),
	}
}

func (d *StoreInfoCollector) Describe(descs chan<- *prometheus.Desc) {
	descs <- d.desc
}

func (d *StoreInfoCollector) Collect(metrics chan<- prometheus.Metric) {
	// Prometheus calls Collect with no ctx, the scrape is the root of its queries
	ctx, cancel := context.WithTimeout(context.Background(), collectTimeout)
	defer cancel()
	for typeUrl, store := range d.stores {
		d.collectStore(ctx, typeUrl, store, metrics)
	}
}

func (d *StoreInfoCollector) collectStore(
	ctx context.Context, typeUrl string, store LightStorer, metrics chan<- prometheus.Metric,
) {
	elems, err := store.LightList(ctx)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list resources")
	}
	for uuid, name := range elems {
		metrics <- prometheus.MustNewConstMetric(
			d.desc,
			prometheus.GaugeValue,
			1,
			typeUrl,
			uuid,
			name,
		)
	}
}

func (d *StoreInfoCollector) AddStore(typeUrl string, store LightStorer) {
	d.stores[typeUrl] = store
}

type ForeignStoreInfoCollector struct {
	desc  *prometheus.Desc
	dbMan *dbs.DbManager
}

func NewForeignStoreInfoCollector(dbMan *dbs.DbManager) *ForeignStoreInfoCollector {
	return &ForeignStoreInfoCollector{
		dbMan: dbMan,
		desc: prometheus.NewDesc(
			"flock_controller_foreign_store_info",
			"Information about foreign resources in the store",
			[]string{"source_type", "source_uuid", "foreign_type", "foreign_uuid"},
			nil,
		),
	}
}

func (d *ForeignStoreInfoCollector) Describe(descs chan<- *prometheus.Desc) {
	descs <- d.desc
}

func (d *ForeignStoreInfoCollector) Collect(metrics chan<- prometheus.Metric) {
	// Prometheus calls Collect with no ctx, the scrape is the root of its queries
	ctx, cancel := context.WithTimeout(context.Background(), collectTimeout)
	defer cancel()
	qb := d.dbMan.Build().NewSelectBuilder()
	qb.Select("source_type", "source_uuid", "foreign_type", "foreign_uuid").From(foreignResourcesTableName)
	rows, err := d.dbMan.Query(ctx, dbs.MakeDbQuery(qb.Build()))
	if err != nil {
		log.Error().Err(err).Msg("Failed to list resources")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var sourceType, sourceUUID, foreignType, foreignUUID string
		err := rows.Scan(&sourceType, &sourceUUID, &foreignType, &foreignUUID)
		if err != nil {
			log.Error().Err(err).Msg("Failed to scan row")
			continue
		}
		metrics <- prometheus.MustNewConstMetric(
			d.desc,
			prometheus.GaugeValue,
			1,
			sourceType,
			sourceUUID,
			foreignType,
			foreignUUID,
		)
	}
}
