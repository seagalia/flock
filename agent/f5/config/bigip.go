package config

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/seagalia/flock/sdk/models"
)

type BigipConfig struct {
	// URLs are the management URLs, one per device of the HA pair, or a single one.
	URLs     []string `yaml:"urls"`
	Username string   `yaml:"username"`
	// PasswordFile holds the password; FLOCK_BIGIP_PASSWORD is read when it is empty. A password is never written in
	// the config file itself.
	PasswordFile string `yaml:"password_file"`
	Password     string `yaml:"-"`

	LoginProvider      string `yaml:"login_provider"`
	CaFile             string `yaml:"ca_file"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`

	DefaultPartition     string `yaml:"default_partition"`
	PartitionMetadataKey string `yaml:"partition_metadata_key"`

	Debounce          models.Duration `yaml:"debounce"`
	MaxDebounce       models.Duration `yaml:"max_debounce"`
	MaxIncomplete     models.Duration `yaml:"max_incomplete"`
	ResyncInterval    models.Duration `yaml:"resync_interval"`
	GarbageAfterStart models.Duration `yaml:"garbage_after_start"`
	RequestTimeout    models.Duration `yaml:"request_timeout"`

	// DryRun makes AS3 validate and plan the declarations without applying them.
	DryRun bool `yaml:"dry_run"`
}

func (c *BigipConfig) UnmarshalYAML(unmarshal func(any) error) error {
	type plain BigipConfig
	err := unmarshal((*plain)(c))
	if err != nil {
		return err
	}
	if len(c.URLs) == 0 {
		return fmt.Errorf("bigip urls is required")
	}
	for _, rawUrl := range c.URLs {
		u, err := url.Parse(rawUrl)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return fmt.Errorf("bigip url %q must be an https url", rawUrl)
		}
	}
	if c.Username == "" {
		return fmt.Errorf("bigip username is required")
	}
	err = c.loadPassword()
	if err != nil {
		return err
	}
	c.applyDefaults()
	return ValidTenantName(c.DefaultPartition)
}

func (c *BigipConfig) loadPassword() error {
	if c.PasswordFile != "" {
		b, err := os.ReadFile(c.PasswordFile)
		if err != nil {
			return fmt.Errorf("read bigip password_file: %w", err)
		}
		c.Password = strings.TrimRight(string(b), "\r\n")
	}
	if c.Password == "" {
		c.Password = os.Getenv("FLOCK_BIGIP_PASSWORD")
	}
	if c.Password == "" {
		return fmt.Errorf("bigip password_file or FLOCK_BIGIP_PASSWORD is required")
	}
	return nil
}

func (c *BigipConfig) applyDefaults() {
	if c.LoginProvider == "" {
		c.LoginProvider = "tmos"
	}
	if c.DefaultPartition == "" {
		c.DefaultPartition = "flock"
	}
	if c.PartitionMetadataKey == "" {
		c.PartitionMetadataKey = "agent.f5.partition"
	}
	defaultDuration(&c.Debounce, time.Second)
	defaultDuration(&c.MaxDebounce, 10*time.Second)
	defaultDuration(&c.MaxIncomplete, time.Minute)
	defaultDuration(&c.ResyncInterval, 5*time.Minute)
	defaultDuration(&c.GarbageAfterStart, 10*time.Minute)
	defaultDuration(&c.RequestTimeout, 2*time.Minute)
}

func defaultDuration(d *models.Duration, value time.Duration) {
	if *d == 0 {
		*d = models.Duration(value)
	}
}

var tenantNameRE = regexp.MustCompile(`^[A-Za-z][0-9A-Za-z_.-]{0,63}$`)

func ValidTenantName(name string) error {
	if !tenantNameRE.MatchString(name) {
		return fmt.Errorf("partition %q is not a valid AS3 tenant name (a letter then up to 63 letters, digits, '_', '.' or '-')", name)
	}
	// Common and Shared are the BIG-IP and AS3 shared partitions, the agent never owns them
	if name == "Common" || name == "Shared" {
		return fmt.Errorf("partition %q is reserved by the BIG-IP", name)
	}
	return nil
}
