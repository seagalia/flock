package instantiaters

import (
	"context"
	"fmt"
	"github.com/hashicorp/go-multierror"
	"github.com/seagalia/flock/controller/config"
	"github.com/seagalia/flock/controller/dbs"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"strings"
)

const (
	serviceInstancesTableName = "service_instances"
)

type KnownInstantiator struct {
	Name                string
	Zones               []string
	OriginatingIdentity string
	ResourceTypes       []*config.ResourceType
	instantiater        Instantiater
}

func (k *KnownInstantiator) CanInstantiate(params InstanceParams) (bool, error) {
	result := false
	for _, resType := range k.ResourceTypes {
		if resType.Name != params.ResourceTypeName {
			continue
		}
		if params.Zone == "" || len(k.Zones) == 0 {
			return true, nil
		}
		for _, zone := range k.Zones {
			if zone != params.Zone {
				continue
			}
			result = true
		}
	}
	if result {
		return true, nil
	}
	return false, fmt.Errorf("resource type %s not supported by instantiator %s", params.ResourceTypeName, k.Name)
}

type DbMulti struct {
	dbMan         *dbs.DbManager
	instantiators []*KnownInstantiator
}

func NewDbMulti(ctx context.Context, dbMan *dbs.DbManager, cnfInsts []*config.Instantiator) (*DbMulti, error) {
	instantiators := make([]*KnownInstantiator, len(cnfInsts))
	for i, cnfInst := range cnfInsts {
		instantiators[i] = &KnownInstantiator{
			Name:                cnfInst.Name,
			Zones:               cnfInst.Zones,
			OriginatingIdentity: cnfInst.OriginatingIdentity,
			ResourceTypes:       cnfInst.ResourceTypes,
			instantiater:        NewStatic(cnfInst.StaticInstantiator.Agents),
		}
	}
	dm := &DbMulti{
		dbMan:         dbMan,
		instantiators: instantiators,
	}
	err := dm.initTables(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to init tables: %w", err)
	}
	return dm, nil
}

func (s *DbMulti) initTables(ctx context.Context) error {
	ctb := s.dbMan.Build().NewCreateTableBuilder()
	ctb.
		CreateTable(serviceInstancesTableName).
		IfNotExists().
		Define("instance_uuid", "CHAR(36)", "NOT NULL", "PRIMARY KEY").
		Define("instantiator_name", "VARCHAR(255)", "NOT NULL").
		Define("current_state", "INTEGER", "NOT NULL").
		Define("desired_state", "INTEGER", "NOT NULL").
		Define("error_message", "TEXT", "NOT NULL")
	_, err := s.dbMan.Exec(ctx, dbs.MakeDbQuery(ctb.Build()))
	if err != nil {
		return fmt.Errorf("failed to exec query: %w", err)
	}
	return nil
}

func (s *DbMulti) checkExist(ctx context.Context, instanceUuid string) (bool, error) {
	var count int
	qb := s.dbMan.Build().NewSelectBuilder()
	qb.Select("COUNT(*)").From(serviceInstancesTableName)
	qb.Where(qb.Equal("instance_uuid", instanceUuid))
	row := s.dbMan.QueryRow(ctx, dbs.MakeDbQuery(qb.Build()))
	err := row.Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *DbMulti) ListAgents(ctx context.Context, instanceUuid string) ([]*agent.Agent, error) {
	exists, err := s.checkExist(ctx, instanceUuid)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("service instance %s not found", instanceUuid)
	}
	inst, err := s.findInstantiatorByInstance(ctx, instanceUuid)
	if err != nil {
		return nil, err
	}
	return inst.instantiater.ListAgents(ctx, instanceUuid)
}

func (s *DbMulti) findInstantiatorByInstance(ctx context.Context, instanceUuid string) (*KnownInstantiator, error) {
	qb := s.dbMan.Build().NewSelectBuilder()
	qb.Select("instantiator_name").From(serviceInstancesTableName)
	qb.Where(qb.Equal("instance_uuid", instanceUuid))
	row := s.dbMan.QueryRow(ctx, dbs.MakeDbQuery(qb.Build()))
	var instName string
	err := row.Scan(&instName)
	if err != nil {
		return nil, err
	}
	return s.findInstantiatorByName(instName)
}

func (s *DbMulti) findInstantiatorByName(name string) (*KnownInstantiator, error) {
	names := make([]string, 0)
	for _, inst := range s.instantiators {
		if inst.Name == name {
			return inst, nil
		}
		names = append(names, inst.Name)
	}
	return nil, fmt.Errorf("no instantiator found with name '%s', available are: '%s'", name, strings.Join(names, "','"))
}

func (s *DbMulti) findInstantiatorByParams(params InstanceParams) (*KnownInstantiator, error) {
	var result error
	for _, inst := range s.instantiators {
		can, err := inst.CanInstantiate(params)
		if err != nil {
			if err != nil {
				result = multierror.Append(result, fmt.Errorf("instantiator %s: %w", inst.Name, err))
				continue
			}
		}
		if can {
			return inst, nil
		}
	}
	return nil, fmt.Errorf("no instantiator found for instance %s: %w", params.InstanceUUID, result)
}

func (s *DbMulti) CanInstantiate(ctx context.Context, params InstanceParams) (bool, error) {
	var result error
	for _, inst := range s.instantiators {
		can, err := inst.CanInstantiate(params)
		if err != nil {
			result = multierror.Append(result, fmt.Errorf("instantiator %s: %w", inst.Name, err))
			continue
		}
		if can {
			return true, nil
		}
	}
	return false, result
}

func (s *DbMulti) CreateServiceInstance(ctx context.Context, params InstanceParams) (*instance.InstanceStatus, error) {
	exists, err := s.checkExist(ctx, params.InstanceUUID)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, fmt.Errorf("service instance %s already exists", params.InstanceUUID)
	}
	inst, err := s.findInstantiatorByParams(params)
	if err != nil {
		return nil, err
	}
	status, err := inst.instantiater.CreateServiceInstance(ctx, params)
	if err != nil {
		return nil, err
	}

	params.OriginatingIdentity = s.makeOriginatingIdentity(inst, params)

	ib := s.dbMan.Build().NewInsertBuilder()
	ib.InsertInto(serviceInstancesTableName)
	ib.Cols("instance_uuid", "instantiator_name", "current_state", "desired_state", "error_message")
	ib.Values(params.InstanceUUID, inst.Name, status.CurrentState, status.DesiredState, status.ErrorMessage)
	_, err = s.dbMan.Exec(ctx, dbs.MakeDbQuery(ib.Build()))
	if err != nil {
		return nil, fmt.Errorf("failed to create service instance on query: %w", err)
	}

	return status, nil
}

func (s *DbMulti) UpdateServiceInstance(ctx context.Context, params InstanceParams) (*instance.InstanceStatus, error) {
	exists, err := s.checkExist(ctx, params.InstanceUUID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("service instance %s not found", params.InstanceUUID)
	}

	newInst, err := s.findInstantiatorByParams(params)
	if err != nil {
		return nil, err
	}
	prevInst, err := s.findInstantiatorByInstance(ctx, params.InstanceUUID)
	if err != nil {
		return nil, err
	}
	if newInst.Name != prevInst.Name {
		return nil, fmt.Errorf("cannot change instantiator for instance %s, you must delete before", params.InstanceUUID)
	}
	params.OriginatingIdentity = s.makeOriginatingIdentity(newInst, params)
	status, err := newInst.instantiater.UpdateServiceInstance(ctx, params)
	if err != nil {
		return nil, err
	}

	err = s.updateInstanceWithStatus(ctx, params.InstanceUUID, status)
	if err != nil {
		return nil, fmt.Errorf("failed to update instance: %w", err)
	}

	return status, nil
}

func (s *DbMulti) updateInstanceWithStatus(ctx context.Context, instanceUuid string, status *instance.InstanceStatus) error {
	ub := s.dbMan.Build().NewUpdateBuilder()
	ub.Update(serviceInstancesTableName)
	ub.Set(ub.Assign("current_state", status.CurrentState))
	ub.Set(ub.Assign("desired_state", status.DesiredState))
	ub.Set(ub.Assign("error_message", status.ErrorMessage))
	ub.Where(ub.Equal("instance_uuid", instanceUuid))
	_, err := s.dbMan.Exec(ctx, dbs.MakeDbQuery(ub.Build()))
	return err
}
func (s *DbMulti) DeleteServiceInstance(ctx context.Context, instanceUuid string) (*instance.InstanceStatus, error) {
	exists, err := s.checkExist(ctx, instanceUuid)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("service instance %s not found", instanceUuid)
	}

	inst, err := s.findInstantiatorByInstance(ctx, instanceUuid)
	if err != nil {
		return nil, err
	}

	status, err := inst.instantiater.DeleteServiceInstance(ctx, instanceUuid)
	if err != nil {
		return nil, err
	}

	db := s.dbMan.Build().NewDeleteBuilder()
	db.DeleteFrom(serviceInstancesTableName).Where(db.Equal("instance_uuid", instanceUuid))
	_, err = s.dbMan.Exec(ctx, dbs.MakeDbQuery(db.Build()))
	if err != nil {
		return nil, fmt.Errorf("failed to delete instance: %w", err)
	}

	err = s.updateInstanceWithStatus(ctx, instanceUuid, status)
	if err != nil {
		return nil, fmt.Errorf("failed to update instance: %w", err)
	}

	return status, nil
}

func (s *DbMulti) GetStatus(ctx context.Context, instanceUuid string) (*instance.InstanceStatus, error) {
	exists, err := s.checkExist(ctx, instanceUuid)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("service instance %s not found", instanceUuid)
	}
	inst, err := s.findInstantiatorByInstance(ctx, instanceUuid)
	if err != nil {
		return nil, err
	}
	status, err := inst.instantiater.GetStatus(ctx, instanceUuid)
	if err != nil {
		return nil, err
	}

	err = s.updateInstanceWithStatus(ctx, instanceUuid, status)
	if err != nil {
		return nil, fmt.Errorf("failed to update instance: %w", err)
	}
	return status, nil
}

func (s *DbMulti) makeOriginatingIdentity(inst *KnownInstantiator, params InstanceParams) string {
	if params.OriginatingIdentity == "" {
		return inst.OriginatingIdentity
	}
	return fmt.Sprintf("%s,%s", inst.OriginatingIdentity, params.OriginatingIdentity)
}
