package remotechan

import "time"

// Clock is the time source the channel reads: token expiry, the heartbeat
// sender and the lost-job monitor all go through it, so a test advances time
// instead of sleeping. The zero value of every option is the real clock.
type Clock interface {
	Now() time.Time
	// Tick delivers the time every d until stop is called.
	Tick(d time.Duration) (c <-chan time.Time, stop func())
}

type realClock struct{}

// RealClock is the wall clock.
func RealClock() Clock { return realClock{} }

func (realClock) Now() time.Time { return time.Now() }

func (realClock) Tick(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}
