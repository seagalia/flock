package params

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/selfhostde"
	"github.com/go-acme/lego/v5/providers/dns/selfhostdev2"
)

type SelfhostdeParams struct {
	Username           string `mapstructure:"username"`
	Password           string `mapstructure:"password"`
	RecordsMapping     string `mapstructure:"records_mapping"`
	TTL                string `mapstructure:"ttl"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *SelfhostdeParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return selfhostde.NewDNSProviderConfig(cnf)
}

func (p *SelfhostdeParams) ToConfig() (*selfhostde.Config, error) {
	cnf := selfhostde.NewDefaultConfig()
	setString(&cnf.Username, p.Username)
	setString(&cnf.Password, p.Password)

	mapping, mappingErr := parseRecordsMapping(p.RecordsMapping)
	err := errors.Join(
		mappingErr,
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("selfhostde: %w", err)
	}

	cnf.RecordsMapping = map[string]*selfhostde.Seq{}
	for domain, ids := range mapping {
		cnf.RecordsMapping[domain] = selfhostde.NewSeq(ids...)
	}
	return cnf, nil
}

type Selfhostdev2Params struct {
	Credentials        string `mapstructure:"credentials"`
	RecordsMapping     string `mapstructure:"records_mapping"`
	TTL                string `mapstructure:"ttl"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *Selfhostdev2Params) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return selfhostdev2.NewDNSProviderConfig(cnf)
}

func (p *Selfhostdev2Params) ToConfig() (*selfhostdev2.Config, error) {
	cnf := selfhostdev2.NewDefaultConfig()

	mapping, mappingErr := parseRecordsMapping(p.RecordsMapping)
	err := errors.Join(
		mappingErr,
		setPairs(&cnf.Credentials, "credentials", p.Credentials),
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("selfhostdev2: %w", err)
	}

	cnf.RecordsMapping = map[string]*selfhostdev2.Seq{}
	for domain, rawIDs := range mapping {
		ids := make([]int64, 0, len(rawIDs))
		for _, rawID := range rawIDs {
			id, err := strconv.ParseInt(rawID, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("selfhostdev2: invalid record id %q of %s: %w", rawID, domain, err)
			}
			ids = append(ids, id)
		}
		cnf.RecordsMapping[domain] = selfhostdev2.NewSeq(ids...)
	}
	return cnf, nil
}

// parseRecordsMapping reads "domain:id1:id2,other.domain:id3", the SELFHOSTDE_RECORDS_MAPPING format of lego: one
// or two record ids per domain. lego keeps its own parser unexported.
func parseRecordsMapping(raw string) (map[string][]string, error) {
	raw = strings.ReplaceAll(raw, " ", "")
	if raw == "" {
		return nil, errors.New("records_mapping is required")
	}

	mapping := map[string][]string{}
	for line := range strings.SplitSeq(raw, ",") {
		parts := strings.Split(line, ":")
		domain, ids := parts[0], parts[1:]
		hasEmptyPart := slices.Contains(parts, "")
		hasOneOrTwoIDs := len(ids) == 1 || len(ids) == 2
		if hasEmptyPart || !hasOneOrTwoIDs {
			return nil, fmt.Errorf("invalid records_mapping entry %q, expected domain:id or domain:id1:id2", line)
		}
		mapping[domain] = ids
	}
	return mapping, nil
}
