package agentlist

import (
	"github.com/hashicorp/memberlist"
	"sync"
	"time"
)

type UptimeDelegate struct {
	startTime time.Time
	mutex     *sync.RWMutex
}

func NewUptimeDelegate() *UptimeDelegate {
	return &UptimeDelegate{
		startTime: time.Now(),
		mutex:     &sync.RWMutex{},
	}
}

func (u *UptimeDelegate) NodeMeta(limit int) []byte {
	u.mutex.RLock()
	defer u.mutex.RUnlock()
	startTime := []byte(u.startTime.Format(time.RFC3339Nano))
	if len(startTime) > limit {
		return startTime[0:limit]
	}
	return startTime
}

func (u *UptimeDelegate) NotifyMsg(bytes []byte) {

}

func (u *UptimeDelegate) GetBroadcasts(overhead, limit int) [][]byte {
	return nil
}

func (u *UptimeDelegate) LocalState(join bool) []byte {
	return nil
}

func (u *UptimeDelegate) MergeRemoteState(buf []byte, join bool) {

}

func (u *UptimeDelegate) ResetStartTime() {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	u.startTime = time.Now()
}

func NodeUptime(node *memberlist.Node) time.Duration {
	t := NodeStartTime(node)
	return time.Since(t)
}

func NodeStartTime(node *memberlist.Node) time.Time {
	t, err := time.Parse(time.RFC3339Nano, string(node.Meta))
	if err != nil {
		return time.Time{}
	}
	return t
}
