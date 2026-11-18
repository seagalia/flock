package waiters

// Waiter pauses the agent work (stop the world) between Suspend and Resume
type Waiter interface {
	Suspend()
	Resume()
	ShouldWait() (bool, chan struct{})
	AddActive()
	RemoveActive()
}
