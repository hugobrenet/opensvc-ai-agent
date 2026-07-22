package api

type askLimiter struct {
	slots chan struct{}
}

func newAskLimiter(maxConcurrent int) *askLimiter {
	return &askLimiter{slots: make(chan struct{}, maxConcurrent)}
}

func (l *askLimiter) tryAcquire() bool {
	select {
	case l.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (l *askLimiter) release() {
	<-l.slots
}
