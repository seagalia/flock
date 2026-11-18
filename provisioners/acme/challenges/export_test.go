package challenges

func (t *Table) WaiterKeys() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.waiters)
}

var ServeHTTP01WithIdleTimeout = serveHTTP01
