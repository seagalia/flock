package ipvsctl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/avast/retry-go/v5"
	"github.com/cloudflare/ipvs"
	"github.com/hashicorp/go-multierror"
)

const (
	// CopiesStateFile records the copies of the wildcard services for the offline clean, on a tmpfs gone at reboot
	// with the ipvs state
	CopiesStateFile = "/run/flock/ipvs-wildcard-copies.json"

	wildcardSyncAttempts  = 5
	wildcardSyncFirstWait = time.Second
)

// MachineIPs gives the addresses of the machine a wildcard service stands for, by family (syscall.AF_INET or
// syscall.AF_INET6)
type MachineIPs interface {
	Current(family int) []net.IP
}

// addrKey is a real service on one address, a copy of a wildcard or a concrete service
type addrKey struct {
	addr     netip.Addr
	port     uint16
	protocol ipvs.Protocol
}

// wildcard is a service on the unspecified address. It lives in no kernel table, its copies do, one per address of
// the machine.
type wildcard struct {
	service      ipvs.Service
	destinations map[string]ipvs.Destination
	copies       map[netip.Addr]ipvs.Service
}

// recordedCopy is a copy as the state file keeps it
type recordedCopy struct {
	Address  string `json:"address"`
	Port     uint16 `json:"port"`
	Protocol uint16 `json:"protocol"`
	Family   uint16 `json:"family"`
}

// WildcardClient serves a service on the unspecified address (0.0.0.0 or ::) as a wildcard copied on every address
// of the machine. Every change of the wildcard and of its destinations reaches each copy, and the listing shows the
// copies as the wildcard alone, so its users keep seeing one service. A concrete service keeps its address, the
// wildcard leaves it until the concrete service is removed.
type WildcardClient struct {
	inner      ipvs.Client
	machineIPs MachineIPs
	statePath  string

	mutex     sync.Mutex
	wildcards map[string]*wildcard
	concretes map[addrKey]bool
	// leftovers are the copies the state file lists from the run before, the only kernel services taken over. They
	// stay recorded until taken over or removed, so an offline clean still finds the ones no wildcard claims again.
	leftovers map[addrKey]ipvs.Service
	// orphans are the copies a removed wildcard failed to remove, the handler never asks again so each sync retries
	orphans map[addrKey]ipvs.Service
}

// NewWildcardClient reads the copies left by the run before in statePath, and records its own copies there
func NewWildcardClient(inner ipvs.Client, machineIPs MachineIPs, statePath string) *WildcardClient {
	c := &WildcardClient{
		inner:      inner,
		machineIPs: machineIPs,
		statePath:  statePath,
		wildcards:  make(map[string]*wildcard),
		concretes:  make(map[addrKey]bool),
		leftovers:  make(map[addrKey]ipvs.Service),
		orphans:    make(map[addrKey]ipvs.Service),
	}
	recorded, err := readCopies(statePath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		entry.Warn().Err(err).Msg("no copy of a wildcard service is taken over from the run before")
	}
	for _, svc := range recorded {
		c.leftovers[keyOf(svc)] = svc
	}
	return c
}

// EnableWildcards puts a WildcardClient over the client in use, the app calls its Sync on each change of the addresses
func EnableWildcards(machineIPs MachineIPs) *WildcardClient {
	wildcardClient := NewWildcardClient(client, machineIPs, CopiesStateFile)
	client = wildcardClient
	return wildcardClient
}

func isWildcard(svc ipvs.Service) bool {
	return svc.Address.Unmap().IsUnspecified()
}

func keyOf(svc ipvs.Service) addrKey {
	return addrKey{addr: svc.Address.Unmap(), port: svc.Port, protocol: svc.Protocol}
}

func destKey(dst ipvs.Destination) string {
	return dst.Address.Unmap().String() + ":" + strconv.Itoa(int(dst.Port))
}

func copyOn(svc ipvs.Service, addr netip.Addr) ipvs.Service {
	svc.Address = addr
	svc.Netmask = MaskFromIPMask(net.CIDRMask(addr.BitLen(), addr.BitLen()))
	return svc
}

func addStats(sum *ipvs.Stats, stats ipvs.Stats) {
	sum.Connections += stats.Connections
	sum.IncomingPackets += stats.IncomingPackets
	sum.OutgoingPackets += stats.OutgoingPackets
	sum.IncomingBytes += stats.IncomingBytes
	sum.OutgoingBytes += stats.OutgoingBytes
	sum.ConnectionRate += stats.ConnectionRate
	sum.IncomingPacketRate += stats.IncomingPacketRate
	sum.OutgoingPacketRate += stats.OutgoingPacketRate
	sum.IncomingByteRate += stats.IncomingByteRate
	sum.OutgoingByteRate += stats.OutgoingByteRate
}

func (c *WildcardClient) Info() (ipvs.Info, error) {
	return c.inner.Info()
}

func (c *WildcardClient) Config() (ipvs.Config, error) {
	return c.inner.Config()
}

func (c *WildcardClient) SetConfig(config ipvs.Config) error {
	return c.inner.SetConfig(config)
}

// Services lists the copies as their wildcard, with the stats of every copy summed
func (c *WildcardClient) Services() ([]ipvs.ServiceExtended, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	services, err := c.inner.Services()
	if err != nil {
		return nil, err
	}
	listed := make([]ipvs.ServiceExtended, 0, len(c.wildcards))
	owners := make(map[addrKey]int)
	for _, w := range c.wildcards {
		listed = append(listed, ipvs.ServiceExtended{Service: w.service})
		for addr := range w.copies {
			owners[addrKey{addr: addr, port: w.service.Port, protocol: w.service.Protocol}] = len(listed) - 1
		}
	}
	others := make([]ipvs.ServiceExtended, 0, len(services))
	for _, service := range services {
		owner, isCopy := owners[keyOf(service.Service)]
		if !isCopy {
			others = append(others, service)
			continue
		}
		addStats(&listed[owner].Stats, service.Stats)
		addStats(&listed[owner].Stats64, service.Stats64)
	}
	return append(listed, others...), nil
}

func (c *WildcardClient) Service(svc ipvs.Service) (ipvs.ServiceExtended, error) {
	if !isWildcard(svc) {
		return c.inner.Service(svc)
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	w, ok := c.wildcards[makeKeyService(svc)]
	if !ok {
		return ipvs.ServiceExtended{}, fmt.Errorf("wildcard service %s not found", makeKeyService(svc))
	}
	listed := ipvs.ServiceExtended{Service: w.service}
	for _, copied := range w.copies {
		service, err := c.inner.Service(copied)
		if err != nil {
			return ipvs.ServiceExtended{}, err
		}
		addStats(&listed.Stats, service.Stats)
		addStats(&listed.Stats64, service.Stats64)
	}
	return listed, nil
}

// CreateService of a wildcard copies it on every address of the machine, a concrete service takes its address from
// the wildcards first
func (c *WildcardClient) CreateService(svc ipvs.Service) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if !isWildcard(svc) {
		return c.setConcrete(svc, c.inner.CreateService)
	}
	key := makeKeyService(svc)
	w, ok := c.wildcards[key]
	if !ok {
		w = &wildcard{
			destinations: make(map[string]ipvs.Destination),
			copies:       make(map[netip.Addr]ipvs.Service),
		}
		c.wildcards[key] = w
	}
	w.service = svc
	defer c.saveCopies()
	return c.expand(w)
}

func (c *WildcardClient) UpdateService(svc ipvs.Service) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if !isWildcard(svc) {
		return c.setConcrete(svc, c.inner.UpdateService)
	}
	w, ok := c.wildcards[makeKeyService(svc)]
	if !ok {
		return fmt.Errorf("wildcard service %s not found", makeKeyService(svc))
	}
	w.service = svc
	var result error
	for addr := range w.copies {
		copied := copyOn(svc, addr)
		if err := c.inner.UpdateService(copied); err != nil {
			result = multierror.Append(result, fmt.Errorf("update copy %s: %w", makeKeyService(copied), err))
			continue
		}
		w.copies[addr] = copied
	}
	return result
}

// setConcrete removes the copies on the address of svc before write sets it. c.mutex must be held.
func (c *WildcardClient) setConcrete(svc ipvs.Service, write func(ipvs.Service) error) error {
	key := keyOf(svc)
	for _, w := range c.wildcards {
		copied, isCopy := w.copies[key.addr]
		if !isCopy || w.service.Port != key.port || w.service.Protocol != key.protocol {
			continue
		}
		if err := c.inner.RemoveService(copied); err != nil {
			return fmt.Errorf("leave %s to a concrete service: %w", makeKeyService(copied), err)
		}
		delete(w.copies, key.addr)
		c.saveCopies()
		entry.Warn().Str("service", makeKeyService(svc)).Str("wildcard", makeKeyService(w.service)).
			Msg("a concrete service takes this address, the wildcard service leaves it to it")
	}
	err := write(svc)
	if err != nil {
		return err
	}
	c.concretes[key] = true
	return nil
}

// RemoveService of a wildcard removes its copies, the removal of a concrete service gives its address back to the
// wildcards on its port
func (c *WildcardClient) RemoveService(svc ipvs.Service) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if !isWildcard(svc) {
		return c.removeConcrete(svc)
	}
	key := makeKeyService(svc)
	w, ok := c.wildcards[key]
	if !ok {
		return fmt.Errorf("wildcard service %s not found", key)
	}
	defer c.saveCopies()
	delete(c.wildcards, key)
	var result error
	for _, copied := range w.copies {
		if err := c.inner.RemoveService(copied); err != nil {
			c.orphans[keyOf(copied)] = copied
			result = multierror.Append(result, fmt.Errorf("remove copy %s: %w", makeKeyService(copied), err))
		}
	}
	return result
}

// removeConcrete also covers a copy removed by its address, as a garbage collection listing from before the copy
// does. The copy is forgotten, then made again by the expansion. c.mutex must be held.
func (c *WildcardClient) removeConcrete(svc ipvs.Service) error {
	if err := c.inner.RemoveService(svc); err != nil {
		return err
	}
	key := keyOf(svc)
	delete(c.concretes, key)
	delete(c.leftovers, key)
	delete(c.orphans, key)
	for _, w := range c.wildcards {
		if w.service.Port == key.port && w.service.Protocol == key.protocol {
			delete(w.copies, key.addr)
		}
	}
	defer c.saveCopies()
	return c.expandOn(key)
}

// Destinations of a wildcard are its own, with the stats of every copy summed
func (c *WildcardClient) Destinations(svc ipvs.Service) ([]ipvs.DestinationExtended, error) {
	if !isWildcard(svc) {
		return c.inner.Destinations(svc)
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	w, ok := c.wildcards[makeKeyService(svc)]
	if !ok {
		return nil, fmt.Errorf("wildcard service %s not found", makeKeyService(svc))
	}
	listed := make(map[string]*ipvs.DestinationExtended, len(w.destinations))
	for key, dst := range w.destinations {
		listed[key] = &ipvs.DestinationExtended{Destination: dst}
	}
	for _, copied := range w.copies {
		dests, err := c.copyDestinations(copied)
		if err != nil {
			return nil, err
		}
		for _, dest := range dests {
			sum, ok := listed[destKey(dest.Destination)]
			if !ok {
				continue
			}
			sum.ActiveConnections += dest.ActiveConnections
			sum.InactiveConnections += dest.InactiveConnections
			sum.PersistentConnections += dest.PersistentConnections
			addStats(&sum.Stats, dest.Stats)
			addStats(&sum.Stats64, dest.Stats64)
		}
	}
	result := make([]ipvs.DestinationExtended, 0, len(listed))
	for _, dest := range listed {
		result = append(result, *dest)
	}
	return result, nil
}

func (c *WildcardClient) CreateDestination(svc ipvs.Service, dst ipvs.Destination) error {
	return c.setDestination(svc, dst, c.inner.CreateDestination)
}

func (c *WildcardClient) UpdateDestination(svc ipvs.Service, dst ipvs.Destination) error {
	return c.setDestination(svc, dst, c.inner.UpdateDestination)
}

// setDestination keeps dst in the wildcard svc and writes it on each copy, or writes it on svc when it is concrete
func (c *WildcardClient) setDestination(
	svc ipvs.Service, dst ipvs.Destination, write func(ipvs.Service, ipvs.Destination) error,
) error {
	if !isWildcard(svc) {
		return write(svc, dst)
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	w, ok := c.wildcards[makeKeyService(svc)]
	if !ok {
		return fmt.Errorf("wildcard service %s not found", makeKeyService(svc))
	}
	w.destinations[destKey(dst)] = dst
	return c.onEachCopy(w, dst, write)
}

func (c *WildcardClient) RemoveDestination(svc ipvs.Service, dst ipvs.Destination) error {
	if !isWildcard(svc) {
		return c.inner.RemoveDestination(svc, dst)
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	w, ok := c.wildcards[makeKeyService(svc)]
	if !ok {
		return fmt.Errorf("wildcard service %s not found", makeKeyService(svc))
	}
	delete(w.destinations, destKey(dst))
	return c.onEachCopy(w, dst, c.inner.RemoveDestination)
}

// onEachCopy writes dst on every copy of w, a copy that fails gets it with the next sync. c.mutex must be held.
func (c *WildcardClient) onEachCopy(
	w *wildcard, dst ipvs.Destination, write func(ipvs.Service, ipvs.Destination) error,
) error {
	var result error
	for _, copied := range w.copies {
		if err := write(copied, dst); err != nil {
			result = multierror.Append(result,
				fmt.Errorf("destination %s of copy %s: %w", destKey(dst), makeKeyService(copied), err))
		}
	}
	return result
}

// Sync follows a change of the addresses of the machine on every wildcard. A failure is retried a few times with a
// doubled wait, since nothing else asks again for a copy that failed.
func (c *WildcardClient) Sync(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}
	return retry.New(
		retry.Context(ctx),
		retry.Attempts(wildcardSyncAttempts),
		retry.Delay(wildcardSyncFirstWait),
		retry.DelayType(retry.BackOffDelay),
		retry.LastErrorOnly(true),
		retry.OnRetry(func(attempt uint, err error) {
			// the caller logs the last failure
			isLast := attempt+1 == wildcardSyncAttempts
			if isLast {
				return
			}
			entry.Warn().Err(err).Uint("attempt", attempt+1).Msg("failed to follow the addresses on the wildcard services")
		}),
	).Do(c.syncOnce)
}

// c.mutex must not be held
func (c *WildcardClient) syncOnce() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	defer c.saveCopies()
	var result error
	for key, orphan := range c.orphans {
		if err := c.inner.RemoveService(orphan); err != nil {
			result = multierror.Append(result, fmt.Errorf("remove copy %s: %w", makeKeyService(orphan), err))
			continue
		}
		delete(c.orphans, key)
	}
	for _, w := range c.wildcards {
		if err := c.expand(w); err != nil {
			result = multierror.Append(result, err)
		}
	}
	return result
}

// c.mutex must be held
func (c *WildcardClient) expandOn(key addrKey) error {
	var result error
	for _, w := range c.wildcards {
		if w.service.Port != key.port || w.service.Protocol != key.protocol {
			continue
		}
		if err := c.expand(w); err != nil {
			result = multierror.Append(result, err)
		}
	}
	return result
}

// wantedAddresses leaves out the addresses a concrete service holds. c.mutex must be held.
func (c *WildcardClient) wantedAddresses(w *wildcard) map[netip.Addr]bool {
	family := syscall.AF_INET
	if w.service.Family == ipvs.INET6 {
		family = syscall.AF_INET6
	}
	wanted := make(map[netip.Addr]bool)
	for _, ip := range c.machineIPs.Current(family) {
		addr := AddrFromIP(ip)
		key := addrKey{addr: addr, port: w.service.Port, protocol: w.service.Protocol}
		if c.concretes[key] {
			entry.Debug().Str("address", addr.String()).Str("wildcard", makeKeyService(w.service)).
				Msg("a concrete service holds this address, the wildcard service leaves it to it")
			continue
		}
		wanted[addr] = true
	}
	return wanted
}

// expand copies w on the addresses of the machine it lacks, removes its copies from the ones gone and gives the
// copies it keeps the destinations a failed write left them without. c.mutex must be held.
func (c *WildcardClient) expand(w *wildcard) error {
	wanted := c.wantedAddresses(w)
	var result error
	for addr, copied := range w.copies {
		if wanted[addr] {
			if err := c.syncDestinations(w, copied); err != nil {
				result = multierror.Append(result, fmt.Errorf("copy on %s: %w", addr, err))
			}
			continue
		}
		if err := c.inner.RemoveService(copied); err != nil {
			result = multierror.Append(result, fmt.Errorf("remove copy %s: %w", makeKeyService(copied), err))
			continue
		}
		delete(w.copies, addr)
	}
	for addr := range wanted {
		if _, ok := w.copies[addr]; ok {
			continue
		}
		copied := copyOn(w.service, addr)
		created, err := c.addCopy(w, copied)
		if created {
			w.copies[addr] = copied
		}
		if err != nil {
			result = multierror.Append(result, fmt.Errorf("copy on %s: %w", addr, err))
		}
	}
	return result
}

// addCopy creates copied with the destinations of w, telling whether the copy is in the kernel. A service already
// there is taken over only when it is a copy of the agent (listed by the state file of the run before, or left by a
// removed wildcard), any other one is left to the concrete service it belongs to.
func (c *WildcardClient) addCopy(w *wildcard, copied ipvs.Service) (bool, error) {
	key := keyOf(copied)
	_, err := c.inner.Service(copied)
	exists := err == nil
	_, isLeftover := c.leftovers[key]
	_, isOrphan := c.orphans[key]
	isOwn := isLeftover || isOrphan
	delete(c.leftovers, key)
	delete(c.orphans, key)
	if exists && !isOwn {
		c.concretes[key] = true
		entry.Warn().Str("service", makeKeyService(copied)).Str("wildcard", makeKeyService(w.service)).
			Msg("a service the agent did not copy holds this address, the wildcard service leaves it to it")
		return false, nil
	}
	if exists {
		err = c.inner.UpdateService(copied)
	} else {
		err = c.inner.CreateService(copied)
	}
	if err != nil {
		return false, err
	}
	return true, c.syncDestinations(w, copied)
}

// syncDestinations makes the destinations of copied in the kernel the ones of w. c.mutex must be held.
func (c *WildcardClient) syncDestinations(w *wildcard, copied ipvs.Service) error {
	dests, err := c.copyDestinations(copied)
	if err != nil {
		return err
	}
	existing := make(map[string]ipvs.Destination, len(dests))
	for _, dest := range dests {
		existing[destKey(dest.Destination)] = dest.Destination
	}
	var result error
	for key, dst := range w.destinations {
		current, ok := existing[key]
		delete(existing, key)
		isSame := ok && current.Weight == dst.Weight && current.FwdMethod == dst.FwdMethod
		if isSame {
			continue
		}
		write := c.inner.CreateDestination
		if ok {
			write = c.inner.UpdateDestination
		}
		if err := write(copied, dst); err != nil {
			result = multierror.Append(result, fmt.Errorf("destination %s: %w", key, err))
		}
	}
	for key, dst := range existing {
		if err := c.inner.RemoveDestination(copied, dst); err != nil {
			result = multierror.Append(result, fmt.Errorf("remove destination %s: %w", key, err))
		}
	}
	return result
}

// the ipvs library answers fs.ErrNotExist for a service with no destination, as a copy is right after its creation
func (c *WildcardClient) copyDestinations(copied ipvs.Service) ([]ipvs.DestinationExtended, error) {
	dests, err := c.inner.Destinations(copied)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return dests, err
}

// RemoveRecordedCopies removes the services statePath lists, then the file, for the clean of a stopped agent. A
// missing or unreadable file only means there is nothing to remove, and so does a listed copy already gone.
func RemoveRecordedCopies(statePath string) error {
	recorded, err := readCopies(statePath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		entry.Warn().Err(err).Msg("no copy of a wildcard service to clean")
		return nil
	}
	var result error
	for _, svc := range recorded {
		_, err := client.Service(svc)
		if err != nil {
			continue
		}
		if err := client.RemoveService(svc); err != nil {
			result = multierror.Append(result, fmt.Errorf("remove copy %s: %w", makeKeyService(svc), err))
		}
	}
	if err := os.Remove(statePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		result = multierror.Append(result, fmt.Errorf("remove the state file of the wildcard copies: %w", err))
	}
	return result
}

func readCopies(statePath string) ([]ipvs.Service, error) {
	raw, err := os.ReadFile(statePath)
	if err != nil {
		return nil, fmt.Errorf("read the state file of the wildcard copies: %w", err)
	}
	var recorded []recordedCopy
	if err := json.Unmarshal(raw, &recorded); err != nil {
		return nil, fmt.Errorf("parse the state file of the wildcard copies %s: %w", statePath, err)
	}
	services := make([]ipvs.Service, 0, len(recorded))
	for _, record := range recorded {
		addr, err := netip.ParseAddr(record.Address)
		if err != nil {
			return nil, fmt.Errorf("parse the state file of the wildcard copies %s: %w", statePath, err)
		}
		services = append(services, copyOn(ipvs.Service{
			Port:     record.Port,
			Protocol: ipvs.Protocol(record.Protocol),
			Family:   ipvs.AddressFamily(record.Family),
		}, addr))
	}
	return services, nil
}

// saveCopies writes the copies through a temporary file renamed over the state file, so a clean never reads half of
// it. A failure only costs the offline clean its list, the copies are served anyway. c.mutex must be held.
func (c *WildcardClient) saveCopies() {
	recorded := make([]recordedCopy, 0)
	for _, w := range c.wildcards {
		for _, copied := range w.copies {
			recorded = append(recorded, recordOf(copied))
		}
	}
	for _, leftover := range c.leftovers {
		recorded = append(recorded, recordOf(leftover))
	}
	for _, orphan := range c.orphans {
		recorded = append(recorded, recordOf(orphan))
	}
	err := writeAtomically(c.statePath, recorded)
	if err != nil {
		entry.Warn().Err(err).Msg("failed to record the copies of the wildcard services, the offline clean misses them")
	}
}

func recordOf(svc ipvs.Service) recordedCopy {
	return recordedCopy{
		Address:  AddrFromIP(svc.Address.AsSlice()).String(),
		Port:     svc.Port,
		Protocol: uint16(svc.Protocol),
		Family:   uint16(svc.Family),
	}
}

func writeAtomically(path string, recorded []recordedCopy) error {
	raw, err := json.Marshal(recorded)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // nolint:errcheck // gone after the rename, only left by a failure
	_, err = tmp.Write(raw)
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp.Name(), path)
}
