package remotechan

import (
	"sync"
	"time"
)

// HeartbeatInterval is how often a running job sends a heartbeat, and how often
// the receiving end checks for jobs gone quiet.
const HeartbeatInterval = 5 * time.Second

// LostAfter is how long a job may go without a heartbeat before it is reported
// lost: three intervals. The check runs every HeartbeatInterval, so a stalled
// job is reported no later than LostAfter+HeartbeatInterval (20s) after its
// last heartbeat — whether or not its connection is still up. A healthy
// connection is not a healthy job.
const LostAfter = 3 * HeartbeatInterval

// JobLost reports a job whose heartbeats stopped.
type JobLost struct {
	Job      string
	LastBeat time.Time
}

// jobTable is the receiving end's view of the jobs it is waiting on: when each
// last beat, so silence is measured per job and not per connection.
type jobTable struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func newJobTable() *jobTable { return &jobTable{last: map[string]time.Time{}} }

// beat records a heartbeat (or the start of the wait) for job. A job reported
// lost was dropped, so a late beat starts it afresh.
func (t *jobTable) beat(job string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.last[job] = now
}

// done forgets a job that finished: silence after it is not a loss.
func (t *jobTable) done(job string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.last, job)
}

// lost returns, once each, the jobs silent for longer than LostAfter at now.
func (t *jobTable) lost(now time.Time) []JobLost {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []JobLost
	for job, at := range t.last {
		if now.Sub(at) > LostAfter {
			out = append(out, JobLost{Job: job, LastBeat: at})
			delete(t.last, job)
		}
	}
	return out
}

// Job is a running job on the sending end. Until Done it sends a heartbeat every
// HeartbeatInterval.
type Job struct {
	name string
	s    *Session
	stop chan struct{}
	exit chan struct{} // closed when the heartbeat goroutine has returned
	once sync.Once
}

// StartJob begins heartbeating job: one at once, then one per HeartbeatInterval.
// Call Done when the job finishes; a job that never says done and stops beating
// is reported lost by the other end.
func (s *Session) StartJob(job string) *Job {
	j := &Job{name: job, s: s, stop: make(chan struct{}), exit: make(chan struct{})}
	go j.beat()
	return j
}

func (j *Job) beat() {
	defer close(j.exit)
	tick, stopTick := j.s.clock.Tick(HeartbeatInterval)
	defer stopTick()
	for {
		if err := j.s.sendApp(j.s.ctx, appMsg{T: msgHeartbeat, Job: j.name}); err != nil {
			return
		}
		select {
		case <-j.stop:
			return
		case <-j.s.done:
			return
		case <-tick:
		}
	}
}

// halt stops the heartbeats without announcing the end: the stall a test needs
// while the connection stays up.
func (j *Job) halt() { j.once.Do(func() { close(j.stop) }) }

// Done ends the job: heartbeats stop and the other end is told it finished, so
// the silence that follows is not reported as a loss. It waits for the last
// heartbeat to leave first, so none can arrive after the done.
func (j *Job) Done() error {
	j.halt()
	<-j.exit
	return j.s.sendApp(j.s.ctx, appMsg{T: msgDone, Job: j.name})
}
