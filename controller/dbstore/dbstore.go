package dbstore

import (
	"context"
	"database/sql"
	goerrors "errors"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"time"

	"github.com/cespare/xxhash/v2"
	sqlbuilder "github.com/huandu/go-sqlbuilder"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
	"github.com/seagalia/flock/controller/dbs"
	"github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/storers"

	"google.golang.org/protobuf/proto"
)

var secretTypeUrl = helpers.TypeUrl[*secret.Secret]()

type foreignResource struct {
	SourceType        string
	SourceIdentifier  string
	ForeignType       string
	ForeignIdentifier string
}

type DbStore[T proto.Message] struct {
	dbMan     *dbs.DbManager
	tableName string
	transHook TransHook
}

func NewDbStore[T proto.Message](
	ctx context.Context,
	dbMan *dbs.DbManager,
	transHook TransHook,
) *DbStore[T] {
	var m T
	ds := &DbStore[T]{
		dbMan:     dbMan,
		tableName: string(m.ProtoReflect().Descriptor().Name()),
		transHook: transHook,
	}
	err := ds.Init(ctx)
	if err != nil {
		log.Fatal().Msgf("failed to init db store: %s", err.Error())
	}
	return ds
}

func (d *DbStore[T]) Init(ctx context.Context) error {
	ctb := d.dbMan.Build().NewCreateTableBuilder()
	blobType := "BLOB"
	if d.dbMan.DbType == dbs.DbTypePostgres {
		blobType = "BYTEA"
	}
	ctb.
		CreateTable(d.tableName).
		IfNotExists().
		Define("uuid", "CHAR(36)", "NOT NULL", "PRIMARY KEY").
		Define("name", "VARCHAR(255)", "NOT NULL").
		Define("signature", "VARCHAR(255)", "NOT NULL").
		Define("data", blobType, "NOT NULL").
		Define("allow_all_read", "BOOLEAN", "NOT NULL", "DEFAULT false").
		Define("created_at", "TIMESTAMP").
		Define("updated_at", "TIMESTAMP")
	_, err := d.dbMan.Exec(ctx, dbs.MakeDbQuery(ctb.Build()))
	if err != nil {
		return fmt.Errorf("failed to create table for %s: %w", helpers.TypeUrl[T](), err)
	}
	return nil
}

func (d *DbStore[T]) checkExist(ctx context.Context, identifier, name string) (bool, error) {
	var count int
	qb := d.dbMan.Build().NewSelectBuilder()
	qb.Select("COUNT(*)").From(d.tableName)
	if name == "" {
		qb.Where(qb.Equal("uuid", identifier))
	} else {
		qb.Where(
			qb.Or(
				qb.Equal("uuid", identifier),
				qb.Equal("name", name),
			),
		)
	}
	row := d.dbMan.QueryRow(ctx, dbs.MakeDbQuery(qb.Build()))
	err := row.Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (d *DbStore[T]) Create(ctx context.Context, message T) error {
	ib := d.dbMan.Build().NewInsertBuilder()
	name := helpers.GetName(message)
	identifier := helpers.GetIdentifier(message)
	exists, err := d.checkExist(ctx, identifier, name)
	if err != nil {
		return fmt.Errorf("failed to check exist with this name or uuid: %w", err)
	}
	if exists {
		return errors.NewErrAlreadyExists(d.tableName, name)
	}

	b, err := proto.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}
	if d.transHook != nil {
		b, err = d.transHook.Convert(b)
		if err != nil {
			return fmt.Errorf("failed to convert message: %w", err)
		}
	}
	sig, err := d.signature(message, b)
	if err != nil {
		return fmt.Errorf("failed to get message signature: %w", err)
	}

	queries := d.makeQueriesFromForeign(d.mapForeignResources(message), map[string]foreignResource{})

	queriesMeta, err := d.makeQueriesFromMetadata(ctx, identifier, helpers.GetMetadata(message), map[string]string{})
	if err != nil {
		return fmt.Errorf("failed to make queries from metadata: %w", err)
	}

	queries = append(queries, queriesMeta...)
	// every driver writes a time.Time in a TIMESTAMP column, not all accept an integer
	createdAt := time.Now().UTC()
	updatedAt := createdAt

	ib.InsertInto(d.tableName).
		Cols("uuid", "name", "signature", "data", "created_at", "updated_at").
		Values(
			identifier,
			name,
			sig,
			b,
			createdAt,
			updatedAt,
		)

	queries = append(queries, dbs.MakeDbQuery(ib.Build()))

	_, err = d.dbMan.ExecTx(ctx, queries)
	if err != nil {
		return fmt.Errorf("failed to create message: %w", err)
	}
	return nil
}

func (d *DbStore[T]) Read(ctx context.Context, identifier string) (T, error) {
	var message T
	exists, err := d.checkExist(ctx, identifier, "")
	if err != nil {
		return message, fmt.Errorf("failed to check exist: %w", err)
	}
	if !exists {
		return message, errors.NewErrNotFound(helpers.TypeUrl[T](), identifier)
	}
	qb := d.dbMan.Build().NewSelectBuilder()
	qb.Select("data").From(d.tableName).Where(qb.Equal("uuid", identifier))
	data := make([]byte, 0)
	row := d.dbMan.QueryRow(ctx, dbs.MakeDbQuery(qb.Build()))
	err = row.Scan(&data)
	if err != nil {
		return message, fmt.Errorf("failed to scan data: %w", err)
	}
	if d.transHook != nil {
		data, err = d.transHook.Revert(data)
		if err != nil {
			return message, fmt.Errorf("failed to revert message: %w", err)
		}
	}
	message = reflect.New(reflect.TypeOf(message).Elem()).Interface().(T)
	err = proto.Unmarshal(data, message)
	if err != nil {
		return message, fmt.Errorf("failed to unmarshal message: %w", err)
	}
	return message, nil
}

func (d *DbStore[T]) Exists(ctx context.Context, identifier string) (bool, error) {
	return d.checkExist(ctx, identifier, "")
}

func (d *DbStore[T]) FindByName(ctx context.Context, name string) (T, error) {
	var message T
	exists, err := d.checkExist(ctx, "", name)
	if err != nil {
		return message, fmt.Errorf("failed to check exist: %w", err)
	}
	if !exists {
		return message, errors.NewErrNotFound(helpers.TypeUrl[T](), name)
	}

	qb := d.dbMan.Build().NewSelectBuilder()
	qb.Select("data").From(d.tableName).Where(qb.Equal("name", name))
	data := make([]byte, 0)
	row := d.dbMan.QueryRow(ctx, dbs.MakeDbQuery(qb.Build()))
	err = row.Scan(&data)
	if err != nil {
		return message, fmt.Errorf("failed to scan data: %w", err)
	}

	if d.transHook != nil {
		data, err = d.transHook.Revert(data)
		if err != nil {
			return message, fmt.Errorf("failed to revert message: %w", err)
		}
	}

	message = reflect.New(reflect.TypeOf(message).Elem()).Interface().(T)
	err = proto.Unmarshal(data, message)
	if err != nil {
		return message, fmt.Errorf("failed to unmarshal message: %w", err)
	}
	return message, nil
}

func (d *DbStore[T]) Update(ctx context.Context, identifier string, message T) error {
	helpers.SetIdentifier(message, identifier)
	exists, err := d.checkExist(ctx, identifier, "")
	if err != nil {
		return fmt.Errorf("failed to check exist: %w", err)
	}
	if !exists {
		return errors.NewErrNotFound(helpers.TypeUrl[T](), identifier)
	}

	name := helpers.GetName(message)
	b, err := proto.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	if d.transHook != nil {
		b, err = d.transHook.Convert(b)
		if err != nil {
			return fmt.Errorf("failed to convert message: %w", err)
		}
	}
	sig, err := d.signature(message, b)
	if err != nil {
		return fmt.Errorf("failed to get message signature: %w", err)
	}

	prevMess, err := d.Read(ctx, identifier)
	if err != nil {
		return fmt.Errorf("failed to read message: %w", err)
	}
	queries := d.makeQueriesFromForeign(d.mapForeignResources(message), d.mapForeignResources(prevMess))

	queriesMeta, err := d.makeQueriesFromMetadata(ctx, identifier, helpers.GetMetadata(message), helpers.GetMetadata(prevMess))
	if err != nil {
		return fmt.Errorf("failed to make queries from metadata: %w", err)
	}

	queries = append(queries, queriesMeta...)

	updatedAt := time.Now().UTC()

	ub := d.dbMan.Build().NewUpdateBuilder()
	ub.Update(d.tableName).
		Set(
			ub.Assign("name", name),
			ub.Assign("signature", sig),
			ub.Assign("data", b),
			ub.Assign("updated_at", updatedAt),
		).Where(ub.Equal("uuid", identifier))

	queries = append(queries, dbs.MakeDbQuery(ub.Build()))

	_, err = d.dbMan.ExecTx(ctx, queries)
	if err != nil {
		return fmt.Errorf("failed to update: %w", err)
	}
	return nil
}

func (d *DbStore[T]) Delete(ctx context.Context, identifier string) error {
	typeUrl := helpers.TypeUrl[T]()
	delRefBuild := d.dbMan.Build().NewDeleteBuilder()
	delRefBuild.DeleteFrom(foreignResourcesTableName).Where(
		delRefBuild.And(
			delRefBuild.Equal("source_type", typeUrl),
			delRefBuild.Equal("source_uuid", identifier),
		),
	)

	delRefPermBuild := d.dbMan.Build().NewDeleteBuilder()
	delRefPermBuild.DeleteFrom(permTableName).Where(
		delRefPermBuild.Equal("resource_type", typeUrl),
		delRefPermBuild.Equal("resource_uuid", identifier),
	)

	delMetaBuild := d.dbMan.Build().NewDeleteBuilder()
	delMetaBuild.DeleteFrom(metaTableName).Where(
		delMetaBuild.Equal("resource_type", typeUrl),
		delMetaBuild.Equal("resource_uuid", identifier),
	)

	db := d.dbMan.Build().NewDeleteBuilder()
	db.DeleteFrom(d.tableName).Where(db.Equal("uuid", identifier))
	_, err := d.dbMan.ExecTx(ctx, []dbs.DbQuery{
		dbs.MakeDbQuery(delRefBuild.Build()),
		dbs.MakeDbQuery(delRefPermBuild.Build()),
		dbs.MakeDbQuery(delMetaBuild.Build()),
		dbs.MakeDbQuery(db.Build()),
	})
	if err != nil {
		return fmt.Errorf("failed to delete: %w", err)
	}
	return nil
}

func (d *DbStore[T]) List(ctx context.Context, opt *storers.ListOptions, filters ...*svccore.ListFilter) ([]T, error) {

	qb := d.dbMan.Build().NewSelectBuilder()
	qb.Select("t.data").From(qb.As(d.tableName, "t"))
	if opt != nil {
		if opt.Limit > 0 {
			qb.Limit(opt.Limit)
		}
		if opt.Offset > 0 {
			qb.Offset(opt.Offset)
		}
		if opt.UserName != "" {
			groupsAny := make([]any, len(opt.UserGroups))
			for i, group := range opt.UserGroups {
				groupsAny[i] = group
			}

			qb.Join(qb.As(permTableName, "p"), "p.resource_uuid = t.uuid")
			qb.GroupBy("t.uuid")
			qb.Where(
				qb.And(
					qb.Equal("p.resource_type", helpers.TypeUrl[T]()),
					qb.GreaterEqualThan("p.role", permission.Role_READER),
					qb.Or(
						qb.And(
							qb.Equal("member_type", permission.MemberType_USER),
							qb.Equal("member_name", opt.UserName),
						),
						qb.And(
							qb.Equal("member_type", permission.MemberType_GROUP),
							qb.In("member_name", groupsAny...),
						),
					),
				),
			)
		}
	}

	addFiltersInSelect(qb, filters...)
	d.addMetaFiltersInSelect(qb, filters...)
	d.addForeignFiltersInSelect(qb, filters...)
	rows, err := d.dbMan.Query(ctx, dbs.MakeDbQuery(qb.Build()))
	if err != nil {
		return nil, fmt.Errorf("failed to query: %w", err)
	}
	defer rows.Close()
	var rawMessage T
	var messages []T
	for rows.Next() {
		var data []byte
		err := rows.Scan(&data)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		message := reflect.New(reflect.TypeOf(rawMessage).Elem()).Interface().(T)
		if d.transHook != nil {
			data, err = d.transHook.Revert(data)
			if err != nil {
				return nil, fmt.Errorf("failed to revert message: %w", err)
			}
		}
		err = proto.Unmarshal(data, message)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal message: %w", err)
		}
		messages = append(messages, message)
	}
	return messages, nil
}

func (d *DbStore[T]) LightList(ctx context.Context) (map[string]string, error) {

	elemsMap := make(map[string]string)

	qb := d.dbMan.Build().NewSelectBuilder()
	qb.Select("uuid", "name").From(d.tableName)
	rows, err := d.dbMan.Query(ctx, dbs.MakeDbQuery(qb.Build()))
	if err != nil {
		return nil, fmt.Errorf("failed to query: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var uuid string
		var name string
		err := rows.Scan(&uuid, &name)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		elemsMap[uuid] = name
	}
	return elemsMap, nil
}

func (d *DbStore[T]) makeQueriesFromMetadata(ctx context.Context, resId string, current, previous map[string]string) ([]dbs.DbQuery, error) {
	if len(current) == 0 && len(previous) == 0 {
		return []dbs.DbQuery{}, nil
	}
	toDelete := make([]string, 0)
	toSet := make(map[string]string)
	maps.Copy(toSet, current)
	for key, value := range previous {
		if _, ok := current[key]; !ok {
			toDelete = append(toDelete, key)
			continue
		}
		if current[key] == value {
			delete(toSet, key)
			continue
		}
	}

	var queries []dbs.DbQuery

	if len(toSet) > 0 {
		for k, v := range toSet {
			setQueries, err := d.setMetadata(ctx, resId, k, v)
			if err != nil {
				return nil, fmt.Errorf("failed to set metadata: %w", err)
			}
			queries = append(queries, setQueries...)
		}

	}

	if len(toDelete) > 0 {
		for _, k := range toDelete {
			queries = append(queries, d.deleteMetaReq(resId, k))
		}

	}
	return queries, nil
}

func (d *DbStore[T]) makeQueriesFromForeign(current, previous map[string]foreignResource) []dbs.DbQuery {
	if len(current) == 0 && len(previous) == 0 {
		return []dbs.DbQuery{}
	}
	toDelete := make([]any, 0)
	for previousId := range previous {
		if _, ok := current[previousId]; !ok {
			toDelete = append(toDelete, previousId)
			continue
		}
		delete(current, previousId)
	}

	var queries []dbs.DbQuery

	if len(toDelete) > 0 {
		delBuild := d.dbMan.Build().NewDeleteBuilder()
		delBuild.DeleteFrom(foreignResourcesTableName).Where(delBuild.In("id", toDelete...))
		queries = append(queries, dbs.MakeDbQuery(delBuild.Build()))
	}

	if len(current) > 0 {
		ib := d.dbMan.Build().NewInsertBuilder()
		ib.InsertInto(foreignResourcesTableName).
			Cols("id", "source_type", "source_uuid", "foreign_type", "foreign_uuid")
		for id, fr := range current {
			ib.Values(
				id,
				fr.SourceType,
				fr.SourceIdentifier,
				fr.ForeignType,
				fr.ForeignIdentifier,
			)
		}
		queries = append(queries, dbs.MakeDbQuery(ib.Build()))
	}
	return queries
}

func (d *DbStore[T]) mapForeignResources(message T) map[string]foreignResource {
	mapFr := make(map[string]foreignResource)
	sourceType := helpers.TypeUrl[T]()
	sourceIdent := helpers.GetIdentifier(message)
	addForeign := func(foreignType, foreignIdent string) {
		fr := foreignResource{
			SourceType:        sourceType,
			SourceIdentifier:  sourceIdent,
			ForeignType:       foreignType,
			ForeignIdentifier: foreignIdent,
		}
		mapFr[d.hashForeign(fr)] = fr
	}

	var messInt any = message
	if foreigner, isForeigner := messInt.(storers.Foreigner); isForeigner {
		for foreignType, foreignIdents := range foreigner.ForeignResources() {
			for _, foreignIdent := range foreignIdents {
				addForeign(foreignType, foreignIdent)
			}
		}
	}

	refs, _ := secret.FindRefs(message)
	for _, found := range refs {
		addForeign(secretTypeUrl, found.Ref.SecretUUID)
	}
	return mapFr
}

// signature hashes the stored bytes of an encrypted store, because a hash of the plain message would let a reader of the
// signature column check a guess of a secret value.
func (d *DbStore[T]) signature(message T, stored []byte) (string, error) {
	if d.transHook == nil {
		return helpers.MessageSignature(message)
	}
	h := xxhash.New()
	h.Write(stored)
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func (d *DbStore[T]) hashForeign(fr foreignResource) string {
	h := xxhash.New()
	h.WriteString(fr.SourceType)
	h.WriteString(fr.SourceIdentifier)
	h.WriteString(fr.ForeignType)
	h.WriteString(fr.ForeignIdentifier)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// FindForeignResources return map of source identifier as key and list of foreign identifiers as value
func (d *DbStore[T]) FindForeignResources(ctx context.Context, typeUrl string) (map[string][]string, error) {
	mapMatch := make(map[string][]string)
	qb := d.dbMan.Build().NewSelectBuilder()
	qb.Select("source_uuid", "foreign_uuid").From(foreignResourcesTableName).Where(qb.And(
		qb.Equal("foreign_type", typeUrl),
		qb.Equal("source_type", helpers.TypeUrl[T]()),
	))
	rows, err := d.dbMan.Query(ctx, dbs.MakeDbQuery(qb.Build()))
	if err != nil {
		return nil, fmt.Errorf("failed to find foreign resources: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var sourceIdent string
		var foreignIdent string
		err := rows.Scan(&sourceIdent, &foreignIdent)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		if _, ok := mapMatch[sourceIdent]; !ok {
			mapMatch[sourceIdent] = []string{}
		}
		mapMatch[sourceIdent] = append(mapMatch[sourceIdent], foreignIdent)
	}

	return mapMatch, nil
}

// FindSrcWithForeignResource returns the source identifiers using the foreign resource identifier
func (d *DbStore[T]) FindSrcWithForeignResource(ctx context.Context, typeUrl string, identifier string) ([]string, error) {
	result := make([]string, 0)
	qb := d.dbMan.Build().NewSelectBuilder()
	qb.Select("source_uuid").From(foreignResourcesTableName).Where(qb.And(
		qb.Equal("source_type", helpers.TypeUrl[T]()),
		qb.Equal("foreign_type", typeUrl),
		qb.Equal("foreign_uuid", identifier),
	))
	rows, err := d.dbMan.Query(ctx, dbs.MakeDbQuery(qb.Build()))
	if err != nil {
		return nil, fmt.Errorf("failed to find source with foreign resource: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var sourceIdent string
		err := rows.Scan(&sourceIdent)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		result = append(result, sourceIdent)
	}
	return result, nil
}

func (d *DbStore[T]) FindForeignBySrc(ctx context.Context, identifier string) (map[string][]string, error) {
	mapMatch := make(map[string][]string, 0)
	qb := d.dbMan.Build().NewSelectBuilder()
	qb.Select("foreign_type", "foreign_uuid").From(foreignResourcesTableName).Where(qb.And(
		qb.Equal("source_type", helpers.TypeUrl[T]()),
		qb.Equal("source_uuid", identifier),
	))
	rows, err := d.dbMan.Query(ctx, dbs.MakeDbQuery(qb.Build()))
	if err != nil {
		return nil, fmt.Errorf("failed to find source with foreign resource: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var foreignType string
		var foreignUuid string
		err := rows.Scan(&foreignType, &foreignUuid)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		if _, ok := mapMatch[foreignType]; !ok {
			mapMatch[foreignType] = []string{}
		}
		mapMatch[foreignType] = append(mapMatch[foreignType], foreignUuid)
	}
	return mapMatch, nil
}

func (d *DbStore[T]) addForeignFiltersInSelect(qb *sqlbuilder.SelectBuilder, filters ...*svccore.ListFilter) {
	filters = lo.Filter[*svccore.ListFilter](filters, func(f *svccore.ListFilter, i int) bool {
		if f.IsMetadata {
			return false
		}
		return strings.HasPrefix(f.Key, helpers.UrlPrefix)
	})
	if len(filters) == 0 {
		return
	}
	subQuerySrc := d.addForeignFiltersInSelectDirect(true, filters...)
	subqueryFr := d.addForeignFiltersInSelectDirect(false, filters...)

	qb.Where(
		qb.Or(
			qb.In("t.uuid", subQuerySrc),
			qb.In("t.uuid", subqueryFr),
		),
	)

}

func (d *DbStore[T]) addForeignFiltersInSelectDirect(
	src bool,
	filters ...*svccore.ListFilter,
) *sqlbuilder.SelectBuilder {
	subQueryForeign := d.dbMan.Build().NewSelectBuilder()

	alias := "frf"
	getKey := "frf.foreign_uuid"
	retTypeKey := "frf.foreign_type"
	lookTypeKey := "frf.source_type"
	lookUuidKey := "frf.source_uuid"
	if !src {
		alias = "frs"
		getKey = "frs.source_uuid"
		retTypeKey = "frs.source_type"
		lookTypeKey = "frs.foreign_type"
		lookUuidKey = "frs.foreign_uuid"
	}

	subQueryForeign.Select(getKey).From(subQueryForeign.As(foreignResourcesTableName, alias))
	multiWhere := make([]string, 0)
	for _, filter := range filters {
		switch filter.Operator {
		case svccore.ListFilter_EQUAL:
			multiWhere = append(multiWhere, subQueryForeign.And(
				subQueryForeign.Equal(lookTypeKey, filter.Key),
				subQueryForeign.Equal(lookUuidKey, filter.Value),
			))
		case svccore.ListFilter_NOT_EQUAL:
			multiWhere = append(multiWhere, subQueryForeign.And(
				subQueryForeign.Equal(lookTypeKey, filter.Key),
				subQueryForeign.Equal(lookUuidKey, filter.Value),
			))
		default:
			continue
		}
	}
	subQueryForeign.Where(
		subQueryForeign.And(
			subQueryForeign.Equal(retTypeKey, helpers.TypeUrl[T]()),
			subQueryForeign.Or(multiWhere...),
		),
	).
		GroupBy(getKey)
	return subQueryForeign
}

// AddPermission adds a permission to a resource
func (d *DbStore[T]) AddPermission(
	ctx context.Context, resId string, role permission.Role,
	memberType permission.MemberType, member string,
) error {
	idPerm := d.hashPerm(resId, role, memberType, member)
	qb := d.dbMan.Build().NewSelectBuilder()
	qb.Select("COUNT(*)").From(permTableName).Where(qb.Equal("id", idPerm))
	var count int
	row := d.dbMan.QueryRow(ctx, dbs.MakeDbQuery(qb.Build()))
	err := row.Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to find permission exists: %w", err)
	}
	if count > 0 {
		return nil
	}
	cb := d.dbMan.Build().NewInsertBuilder()
	cb.InsertInto(permTableName).
		Cols("id", "resource_type", "resource_uuid", "role", "member_type", "member_name").
		Values(idPerm, helpers.TypeUrl[T](), resId, role, memberType, member)
	_, err = d.dbMan.ExecTx(ctx, []dbs.DbQuery{
		d.deletePermReq(resId, memberType, member),
		dbs.MakeDbQuery(cb.Build()),
	})
	if err != nil {
		return fmt.Errorf("failed to add permission: %w", err)
	}
	return nil
}

func (d *DbStore[T]) deletePermReq(resId string,
	memberType permission.MemberType, member string,
) dbs.DbQuery {
	qb := d.dbMan.Build().NewDeleteBuilder()
	qb.DeleteFrom(permTableName).Where(
		qb.Equal("resource_type", helpers.TypeUrl[T]()),
		qb.Equal("resource_uuid", resId),
		qb.Equal("member_type", memberType),
		qb.Equal("member_name", member),
	)
	return dbs.MakeDbQuery(qb.Build())
}

// DeletePermission removes a permission from a resource
func (d *DbStore[T]) DeletePermission(
	ctx context.Context, resId string,
	memberType permission.MemberType, member string,
) error {
	_, err := d.dbMan.Exec(ctx, d.deletePermReq(resId, memberType, member))
	if err != nil {
		return fmt.Errorf("failed to remove permission: %w", err)
	}
	return nil
}

// SetAllowAllRead sets allow all read flag on a resource
func (d *DbStore[T]) SetAllowAllRead(ctx context.Context, resId string, allowAll bool) error {
	ub := d.dbMan.Build().NewUpdateBuilder()
	updatedAt := time.Now().UTC()
	ub.Update(d.tableName).
		Set(
			ub.Assign("allow_all_read", allowAll),
			ub.Assign("updated_at", updatedAt),
		).Where(ub.Equal("uuid", resId))
	_, err := d.dbMan.Exec(ctx, dbs.MakeDbQuery(ub.Build()))
	if err != nil {
		return fmt.Errorf("failed to set allow all read: %w", err)
	}
	return nil
}

func (d *DbStore[T]) IsAllowAllRead(ctx context.Context, resId string) (bool, error) {
	qbAllowAll := d.dbMan.Build().NewSelectBuilder()
	qbAllowAll.Select("allow_all_read").From(d.tableName).Where(qbAllowAll.Equal("uuid", resId))
	var allowAll bool
	row := d.dbMan.QueryRow(ctx, dbs.MakeDbQuery(qbAllowAll.Build()))
	err := row.Scan(&allowAll)
	if err != nil {
		// an unknown resource is readable by nobody, so a caller learns nothing of its existence from the answer
		if goerrors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check permission: %w", err)
	}
	return allowAll, nil
}

// HasPermission returns true if the user has the permission on the resource
func (d *DbStore[T]) HasPermission(
	ctx context.Context, resId string, role permission.Role,
	username string, groups []string,
) (bool, error) {

	if username == "" && len(groups) == 0 {
		return false, fmt.Errorf("failed to check permission, username or/and groups must be set")
	}

	// first check if the resource allow any read when the role is READER
	if role == permission.Role_READER {
		allowAll, err := d.IsAllowAllRead(ctx, resId)
		if err != nil {
			return false, err
		}
		if allowAll {
			return true, nil
		}
	}

	groupsAny := make([]any, len(groups))
	for i, g := range groups {
		groupsAny[i] = g
	}

	qbPerm := d.dbMan.Build().NewSelectBuilder()
	groupsOrUser := make([]string, 0)
	if username != "" {
		groupsOrUser = append(groupsOrUser, qbPerm.And(
			qbPerm.Equal("member_type", permission.MemberType_USER),
			qbPerm.Equal("member_name", username),
		))
	}
	if len(groups) > 0 {
		groupsOrUser = append(groupsOrUser, qbPerm.And(
			qbPerm.Equal("member_type", permission.MemberType_GROUP),
			qbPerm.In("member_name", groupsAny...),
		))
	}

	qbPerm.Select("COUNT(*)").From(permTableName).Where(
		qbPerm.And(
			qbPerm.Equal("resource_type", helpers.TypeUrl[T]()),
			qbPerm.Equal("resource_uuid", resId),
			qbPerm.GreaterEqualThan("role", role),
			qbPerm.Or(groupsOrUser...),
		),
	)
	var count int
	row := d.dbMan.QueryRow(ctx, dbs.MakeDbQuery(qbPerm.Build()))
	err := row.Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check permission: %w", err)
	}

	return count > 0, nil
}

func (d *DbStore[T]) ListMemberPermissions(ctx context.Context, resId string, role permission.Role) ([]*permission.MemberPermission, error) {
	qb := d.dbMan.Build().NewSelectBuilder()

	whereAnd := []string{
		qb.Equal("resource_type", helpers.TypeUrl[T]()),
		qb.Equal("resource_uuid", resId),
	}
	if role >= 0 {
		whereAnd = append(whereAnd, qb.Equal("role", role))
	}

	qb.Select("role", "member_type", "member_name").From(permTableName).Where(
		qb.And(
			whereAnd...,
		),
	).OrderBy("role")
	rows, err := d.dbMan.Query(ctx, dbs.MakeDbQuery(qb.Build()))
	if err != nil {
		return nil, fmt.Errorf("failed to query: %w", err)
	}
	defer rows.Close()
	permMembers := make([]*permission.MemberPermission, 0)
	for rows.Next() {
		var role int
		var memberType int
		var memberName string
		err := rows.Scan(&role, &memberType, &memberName)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		permMembers = append(permMembers, &permission.MemberPermission{
			Role: permission.Role(role),
			Member: &permission.Member{
				MemberType: permission.MemberType(memberType),
				MemberName: memberName,
			},
		})
	}
	return permMembers, nil
}

func (d *DbStore[T]) hashPerm(
	resId string, role permission.Role,
	memberType permission.MemberType, member string,
) string {
	h := xxhash.New()
	h.WriteString(helpers.TypeUrl[T]())
	h.WriteString(resId)
	h.WriteString(role.String())
	h.WriteString(memberType.String())
	h.WriteString(member)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func (d *DbStore[T]) hashMetadata(resId string, key string) string {
	h := xxhash.New()
	h.WriteString(helpers.TypeUrl[T]())
	h.WriteString(resId)
	h.WriteString(key)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func addFiltersInSelect(qb *sqlbuilder.SelectBuilder, filters ...*svccore.ListFilter) {
	filters = lo.Filter[*svccore.ListFilter](filters, func(f *svccore.ListFilter, i int) bool {
		return !f.IsMetadata
	})
	if len(filters) == 0 {
		return
	}
	sqlFilters := make([]string, 0)
	for _, filter := range filters {
		if filter.Key != "name" && filter.Key != "uuid" {
			continue
		}
		if filter.Key == "name" {
			switch filter.Operator {
			case svccore.ListFilter_EQUAL:
				sqlFilters = append(sqlFilters, qb.Equal("t.name", filter.Value))
			case svccore.ListFilter_NOT_EQUAL:
				sqlFilters = append(sqlFilters, qb.NotEqual("t.name", filter.Value))
			case svccore.ListFilter_LIKE:
				sqlFilters = append(sqlFilters, qb.Like("t.name", "%"+filter.Value+"%"))
			case svccore.ListFilter_NOT_LIKE:
				sqlFilters = append(sqlFilters, qb.NotLike("t.name", "%"+filter.Value+"%"))
			}
		}
		if filter.Key == "uuid" {
			switch filter.Operator {
			case svccore.ListFilter_EQUAL:
				sqlFilters = append(sqlFilters, qb.Equal("t.uuid", filter.Value))
			case svccore.ListFilter_NOT_EQUAL:
				sqlFilters = append(sqlFilters, qb.NotEqual("t.uuid", filter.Value))
			case svccore.ListFilter_LIKE:
				sqlFilters = append(sqlFilters, qb.Like("t.uuid", "%"+filter.Value+"%"))
			case svccore.ListFilter_NOT_LIKE:
				sqlFilters = append(sqlFilters, qb.NotLike("t.uuid", "%"+filter.Value+"%"))
			}
		}

	}
	qb.Where(qb.Or(sqlFilters...))
}

func (d *DbStore[T]) addMetaFiltersInSelect(qb *sqlbuilder.SelectBuilder, filters ...*svccore.ListFilter) {
	filters = lo.Filter[*svccore.ListFilter](filters, func(f *svccore.ListFilter, i int) bool {
		return f.IsMetadata
	})
	if len(filters) == 0 {
		return
	}

	subQuery := d.dbMan.Build().NewSelectBuilder()
	subQuery.Select("m.resource_uuid").From(subQuery.As("metadata", "m"))
	multiWhere := make([]string, 0)
	for _, filter := range filters {
		switch filter.Operator {
		case svccore.ListFilter_EQUAL:
			multiWhere = append(multiWhere, subQuery.And(
				subQuery.Equal("m.name", filter.Key),
				subQuery.Equal("m.value", filter.Value),
			))
		case svccore.ListFilter_NOT_EQUAL:
			multiWhere = append(multiWhere, subQuery.And(
				subQuery.Equal("m.name", filter.Key),
				subQuery.NotEqual("m.value", filter.Value),
			))
		case svccore.ListFilter_LIKE:
			multiWhere = append(multiWhere, subQuery.And(
				subQuery.Equal("m.name", filter.Key),
				subQuery.Like("m.value", "%"+filter.Value+"%"),
			))
		case svccore.ListFilter_NOT_LIKE:
			multiWhere = append(multiWhere, subQuery.And(
				subQuery.Equal("m.name", filter.Key),
				subQuery.NotLike("m.value", "%"+filter.Value+"%"),
			))
		}
	}
	subQuery.Where(
		subQuery.And(
			subQuery.Equal("m.resource_type", helpers.TypeUrl[T]()),
			subQuery.Or(multiWhere...),
		),
	).
		GroupBy("m.resource_uuid").
		Having(subQuery.Equal("COUNT(*)", len(filters)))
	qb.Where(qb.In("t.uuid", subQuery))
}

func (d *DbStore[T]) setMetadata(ctx context.Context, resId string, key, value string) ([]dbs.DbQuery, error) {
	idMeta := d.hashMetadata(resId, key)
	qb := d.dbMan.Build().NewSelectBuilder()
	qb.Select("COUNT(*)").From(metaTableName).Where(qb.Equal("id", idMeta))
	var count int
	row := d.dbMan.QueryRow(ctx, dbs.MakeDbQuery(qb.Build()))
	err := row.Scan(&count)
	if err != nil {
		return nil, fmt.Errorf("failed to find metadata exists: %w", err)
	}
	if count > 0 {
		ub := d.dbMan.Build().NewUpdateBuilder()
		ub.Update(metaTableName).
			Set(ub.Assign("value", value)).
			Where(ub.Equal("id", idMeta))

		return []dbs.DbQuery{
			dbs.MakeDbQuery(ub.Build()),
		}, err
	}
	cb := d.dbMan.Build().NewInsertBuilder()
	cb.InsertInto(metaTableName).
		Cols("id", "resource_type", "resource_uuid", "name", "value").
		Values(idMeta, helpers.TypeUrl[T](), resId, key, value)
	return []dbs.DbQuery{
		d.deleteMetaReq(resId, key),
		dbs.MakeDbQuery(cb.Build()),
	}, nil
}

func (d *DbStore[T]) deleteMetaReq(resId string, key string) dbs.DbQuery {
	qb := d.dbMan.Build().NewDeleteBuilder()
	qb.DeleteFrom(metaTableName).Where(
		qb.Equal("resource_type", helpers.TypeUrl[T]()),
		qb.Equal("resource_uuid", resId),
		qb.Equal("name", key),
	)
	return dbs.MakeDbQuery(qb.Build())
}
