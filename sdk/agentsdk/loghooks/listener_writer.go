package loghooks

import (
	"container/ring"
	"fmt"
	"sync"
)

type LogListener interface {
	OnLogReceive(logLine string)
}

// ListenerWriter keeps the last 500 log lines it receives and pushes every new one to its listeners (logs service)
type ListenerWriter struct {
	listeners  *sync.Map
	ringBuffer *ring.Ring
	rwMutex    *sync.RWMutex
}

func NewListenerWriter() *ListenerWriter {
	return &ListenerWriter{
		listeners:  &sync.Map{},
		ringBuffer: ring.New(500),
		rwMutex:    &sync.RWMutex{},
	}
}

func (w *ListenerWriter) AddListener(listener LogListener) {
	w.listeners.LoadOrStore(fmt.Sprintf("%p", listener), listener)
}

func (w *ListenerWriter) RemoveListener(listener LogListener) {
	w.listeners.Delete(fmt.Sprintf("%p", listener))
}

func (w *ListenerWriter) LatestLines() []string {
	w.rwMutex.RLock()
	defer w.rwMutex.RUnlock()
	logLines := make([]string, 0)
	w.ringBuffer.Do(func(value any) {
		if value != nil {
			logLines = append(logLines, value.(string))
		}
	})
	return logLines
}

func (w *ListenerWriter) Write(line []byte) (int, error) {
	// the caller reuses line once Write returns
	logLine := string(line)
	w.rwMutex.Lock()
	w.ringBuffer.Value = logLine
	w.ringBuffer = w.ringBuffer.Next()
	w.rwMutex.Unlock()

	w.listeners.Range(func(key, value any) bool {
		go value.(LogListener).OnLogReceive(logLine)
		return true
	})
	return len(line), nil
}
