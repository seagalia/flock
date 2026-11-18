package serves

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/seagalia/flock/sdk/storers"
)

const (
	nodeRefreshEvery = 30 * time.Second
	// nodeStaleAfter leaves three refreshes to a controller before another address can take over its node
	nodeStaleAfter   = 90 * time.Second
	nodeTouchTimeout = 10 * time.Second
)

type nodeKey struct {
	instanceUuid string
	agentUuid    string
}

type presenceEntry struct {
	streams int
	addr    string
	timer   *time.Timer
}

// nodePresence keeps the last seen time of the nodes holding at least one stream on this controller fresh, so a second
// process using the same identity from another address stays refused while the first one is connected.
type nodePresence struct {
	nodes        storers.NodeStore
	refreshEvery time.Duration
	entry        zerolog.Logger
	mutex        sync.Mutex
	entries      map[nodeKey]*presenceEntry
}

func newNodePresence(nodes storers.NodeStore, refreshEvery time.Duration, entry zerolog.Logger) *nodePresence {
	return &nodePresence{
		nodes:        nodes,
		refreshEvery: refreshEvery,
		entry:        entry,
		entries:      map[nodeKey]*presenceEntry{},
	}
}

// open counts a stream of the node connected from addr, the first one starts the refresh
func (p *nodePresence) open(ctx context.Context, key nodeKey, addr string) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if current, ok := p.entries[key]; ok {
		current.streams++
		return
	}
	opened := &presenceEntry{streams: 1, addr: addr}
	opened.timer = time.AfterFunc(p.refreshEvery, func() { p.refresh(ctx, key, opened) })
	p.entries[key] = opened
}

// close uncounts a stream of the node, the last one stops the refresh and touches the node once more
func (p *nodePresence) close(ctx context.Context, key nodeKey) {
	p.mutex.Lock()
	current, ok := p.entries[key]
	if !ok {
		p.mutex.Unlock()
		return
	}
	current.streams--
	if current.streams > 0 {
		p.mutex.Unlock()
		return
	}
	current.timer.Stop()
	delete(p.entries, key)
	p.mutex.Unlock()
	p.touch(ctx, key, current.addr)
}

func (p *nodePresence) refresh(ctx context.Context, key nodeKey, refreshed *presenceEntry) {
	p.touch(ctx, key, refreshed.addr)
	p.mutex.Lock()
	defer p.mutex.Unlock()
	// the node may have closed its last stream, or closed and opened again, while it was touched
	if p.entries[key] != refreshed {
		return
	}
	refreshed.timer.Reset(p.refreshEvery)
}

// touch runs detached from the cancellation of ctx: the refresh outlives the stream that opened the node, and the last
// touch comes once the last stream is gone
func (p *nodePresence) touch(ctx context.Context, key nodeKey, addr string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), nodeTouchTimeout)
	defer cancel()
	err := p.nodes.TouchNode(ctx, key.instanceUuid, key.agentUuid, addr)
	if err != nil {
		p.entry.Warn().Err(err).
			Str("instance_uuid", key.instanceUuid).
			Str("agent_uuid", key.agentUuid).
			Str("addr", addr).
			Msg("Failed to refresh the last seen time of a node")
	}
}
