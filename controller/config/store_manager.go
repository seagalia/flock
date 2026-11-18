package config

import (
	"fmt"
	"github.com/seagalia/flock/sdk/config"
	"strings"
	"time"
)

// StoreManagerConfig is always a sql database: sqlite stands for a local file, and the instantiators, the
// permissions, the internal ipam and the locks shared between controllers all live in the database.
type StoreManagerConfig struct {
	SqlStorer *SqlStorer `yaml:"sql"`
}

type SqlStorer struct {
	DbType         string          `yaml:"type"`
	DSN            string          `yaml:"dsn"`
	CnxMaxIdle     int             `yaml:"cnx_max_idle"`
	CnxMaxOpen     int             `yaml:"cnx_max_open"`
	CnxMaxLifetime config.Duration `yaml:"cnx_max_lifetime"`
	CnxMaxIdleTime config.Duration `yaml:"cnx_max_idle_time"`
	Secret         string          `yaml:"secret_key"`
}

func (u *SqlStorer) UnmarshalYAML(unmarshal func(any) error) error {
	type plain SqlStorer
	err := unmarshal((*plain)(u))
	if err != nil {
		return err
	}
	if u.DbType == "" {
		return fmt.Errorf("sql storer config: type is required")
	}
	if u.DSN == "" {
		return fmt.Errorf("sql storer config: dsn is required")
	}
	if u.Secret == "" {
		return fmt.Errorf("sql storer config: secret_key is required")
	}
	if u.CnxMaxIdle <= 0 {
		u.CnxMaxIdle = 10
	}
	if u.CnxMaxOpen <= 0 {
		u.CnxMaxOpen = 10
	}
	if u.CnxMaxLifetime <= 0 {
		u.CnxMaxLifetime = config.Duration(1 * time.Hour)
	}
	if u.CnxMaxIdleTime <= 0 {
		u.CnxMaxIdleTime = config.Duration(20 * time.Minute)
	}
	u.DbType = strings.ToLower(u.DbType)
	return nil
}
