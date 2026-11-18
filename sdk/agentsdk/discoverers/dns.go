package discoverers

import (
	"context"
	"errors"
	"fmt"
	"github.com/cespare/xxhash/v2"
	"github.com/miekg/dns"
	"github.com/rs/zerolog/log"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"net"
	"strconv"
	"sync"
	"time"
)

const (
	resolvConf  = "/etc/resolv.conf"
	DefaultPort = uint32(53)
)

var cacheDnsAnswer = &sync.Map{}

func ClearCacheDns() {
	cacheDnsAnswer.Range(func(key, value any) bool {
		cacheDnsAnswer.Delete(key)
		return true
	})
}

type dnsResponse struct {
	resp  *dns.Msg
	setAt time.Time
}

type Dns struct {
	host         string
	discoverType backend.DNS_LookupFamily
	confDns      *dns.ClientConfig
	port         uint32
}

func NewDns(host string, port uint32, discoverType backend.DNS_LookupFamily, dnsServers ...*core.IPAddr) (*Dns, error) {
	var err error
	var conf *dns.ClientConfig
	if len(dnsServers) == 0 {
		conf, err = dns.ClientConfigFromFile(resolvConf)
		if err != nil {
			return nil, fmt.Errorf("could not load resolv.conf: %s", err.Error())
		}
		servers := conf.Servers
		for i, server := range conf.Servers {
			_, _, err := net.SplitHostPort(server)
			if err == nil {
				continue
			}
			servers[i] = net.JoinHostPort(server, fmt.Sprintf("%d", DefaultPort))
		}
	} else {
		servers := make([]string, 0)
		for _, ip := range dnsServers {
			if ip.GetIp() == "" {
				continue
			}
			port := DefaultPort
			if ip.GetPort() != 0 {
				port = ip.GetPort()
			}
			servers = append(servers, ip.GetIp()+":"+strconv.FormatUint(uint64(port), 10))
		}
		conf = &dns.ClientConfig{
			Servers:  servers,
			Search:   make([]string, 0),
			Ndots:    1,
			Timeout:  5,
			Attempts: 2,
		}
	}
	return &Dns{
		host:         host,
		discoverType: discoverType,
		confDns:      conf,
		port:         port,
	}, nil
}

func (d *Dns) Run(ctx context.Context, found chan<- []*core.IPAddr) error {
	err := d.refreshOne(ctx, d.host, found)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (d *Dns) findTargets(ctx context.Context, name string, queryType uint16) ([]*core.IPAddr, error) {
	response, err := d.lookupWithSearchPath(ctx, name, queryType)
	if err != nil {
		return nil, err
	}

	targetsMap := make(map[string]*core.IPAddr)
	for _, record := range response.Answer {
		switch addr := record.(type) {
		case *dns.A:
			targetsMap[addr.A.String()] = &core.IPAddr{
				Ip:   addr.A.String(),
				Port: d.port,
			}
		case *dns.AAAA:
			targetsMap[addr.AAAA.String()] = &core.IPAddr{
				Ip:   addr.AAAA.String(),
				Port: d.port,
			}
		case *dns.CNAME:
			// CNAME responses can occur with "QueryType: A" dns_sd_config requests.
			continue
		default:
			log.Warn().Stringer("record", record).Msg("Invalid record")
			continue
		}
	}
	targets := make([]*core.IPAddr, 0)
	for _, target := range targetsMap {
		targets = append(targets, target)
	}
	return targets, nil
}

func (d *Dns) refreshOne(ctx context.Context, name string, ch chan<- []*core.IPAddr) error {
	var queryType = dns.TypeA
	if d.discoverType == backend.DNS_V6_PREFERRED || d.discoverType == backend.DNS_V6_ONLY {
		queryType = dns.TypeAAAA
	}
	if d.discoverType == backend.DNS_V4_PREFERRED || d.discoverType == backend.DNS_V4_ONLY {
		queryType = dns.TypeA
	}
	targets, err := d.findTargets(ctx, name, queryType)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		if d.discoverType == backend.DNS_V6_PREFERRED {
			targets, err = d.findTargets(ctx, name, dns.TypeA)
			if err != nil {
				return err
			}
		}
		if d.discoverType == backend.DNS_V4_PREFERRED {
			targets, err = d.findTargets(ctx, name, dns.TypeAAAA)
			if err != nil {
				return err
			}
		}
	}
	if d.discoverType == backend.DNS_ALL {
		addTargets, err := d.findTargets(ctx, name, dns.TypeAAAA)
		if err != nil {
			return err
		}
		targets = append(targets, addTargets...)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case ch <- targets:
	}

	return nil
}

// this part has been taken from https://github.com/prometheus/prometheus/blob/main/discovery/dns/dns.go
// license apache 2.0 from The Prometheus

// lookupWithSearchPath tries name with each search domain appended. It gives the first valid answer, an empty
// answer when every server says NXDOMAIN for every name, and an error when a name got only errors: the current
// state must then be kept until the name resolves.
func (d *Dns) lookupWithSearchPath(ctx context.Context, name string, qtype uint16) (*dns.Msg, error) {
	allResponsesValid := true
	for _, lname := range d.confDns.NameList(name) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		response, err := d.lookupFromAnyServer(ctx, lname, qtype)

		if err != nil {
			// a later name may still answer, but every name being NXDOMAIN can no longer be concluded
			allResponsesValid = false
		} else if response.Rcode == dns.RcodeSuccess {
			return response, nil
		}
	}

	if allResponsesValid {
		// every name is NXDOMAIN: there is no record
		return &dns.Msg{}, nil
	}
	return nil, fmt.Errorf("could not resolve %q: all servers responded with errors to at least one search domain", name)
}

// lookupFromAnyServer gives the first viable answer of the configured servers: records, no record of qtype
// (SUCCESS) or NXDOMAIN. Anything else (timeout, SERVFAIL, REFUSED...) makes it try the next server.
func (d *Dns) lookupFromAnyServer(ctx context.Context, name string, qtype uint16) (*dns.Msg, error) {
	client := &dns.Client{}
	for _, server := range d.confDns.Servers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		client.Net = "udp"
		timeout := time.Duration(d.confDns.Timeout) * time.Second
		queryCtx, cancel := context.WithTimeout(ctx, timeout)
		msg, err := d.askServerForName(queryCtx, name, qtype, client, server, true, 1*time.Second)
		cancel()
		// only a cancel of the caller stops here, a timeout of the query alone may still be answered from the cache
		// and tries the next server
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if err != nil {
			log.Warn().
				Str("server", server).
				Str("name", name).
				Msgf("DNS resolution failed: %s", err.Error())
			continue
		}
		if msg.Rcode == dns.RcodeSuccess {
			return msg, nil
		}
	}

	return nil, fmt.Errorf("could not resolve %s: no servers returned a viable answer", name)
}

// askServerForName asks one server, over TCP again when the answer is truncated.
func (d *Dns) askServerForName(ctx context.Context, name string, queryType uint16, client *dns.Client, servAddr string, edns bool, exp time.Duration) (*dns.Msg, error) {
	msg := &dns.Msg{}
	msg.Id = dns.Id()
	msg.SetQuestion(dns.Fqdn(name), queryType)
	if edns {
		msg.SetEdns0(dns.DefaultMsgSize, true)
	}
	startAt := time.Now()
	response, err := exchange(ctx, client, msg, servAddr)
	if err != nil {
		respCache, ok := cacheDnsAnswer.Load(hash(name, queryType, servAddr))
		if ok {
			dnsResp := respCache.(*dnsResponse)
			if !dnsResp.setAt.After(startAt.Add(-exp)) {
				log.Warn().
					Str("name", name).
					Uint16("queryType", queryType).
					Str("servAddr", servAddr).
					Msgf("error when getting response, use cache instead: %s", err.Error())
			}

			return dnsResp.resp, nil
		}
		return nil, err
	}
	if response.Truncated {
		if client.Net == "tcp" {
			return nil, errors.New("got truncated message on TCP (64kiB limit exceeded?)")
		}
		client.Net = "tcp"
		return d.askServerForName(ctx, name, queryType, client, servAddr, false, exp)
	}
	cacheDnsAnswer.Store(hash(name, queryType, servAddr), &dnsResponse{
		resp:  response,
		setAt: time.Now(),
	})

	return response, nil
}

// exchange sends msg and reads the answer. ExchangeContext only honors the deadline of ctx, so the connection is closed
// when ctx ends to wake up a read on a server that never answers.
func exchange(ctx context.Context, client *dns.Client, msg *dns.Msg, servAddr string) (*dns.Msg, error) {
	conn, err := client.DialContext(ctx, servAddr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	response, _, err := client.ExchangeWithConnContext(ctx, msg, conn)
	return response, err
}

func hash(qname string, qtype uint16, servAddr string) uint64 {
	h := xxhash.New()
	h.Write([]byte{byte(qtype >> 8)})
	h.Write([]byte{byte(qtype)})
	h.Write([]byte(qname))
	h.Write([]byte(servAddr))
	return h.Sum64()
}
