package remotenode

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/bobmcallan/satelle/internal/remotechan"
)

// A secret is requested by the node's agent and answered by local satelle, in
// the job the agent is working: the node sends typeSecretGet in the job's own
// message stream and local satelle answers in the same job, so the answer is
// bound to that job (and, through it, to the run local satelle registered for
// it). The value travels only inside the sealed channel and lives only in the
// memory of the process that asked for it.
const (
	typeSecretGet    = "secret.get"    // node → local: one key, one nonce, inside a job
	typeSecretValue  = "secret.value"  // local → node: the value for that nonce
	typeSecretDenied = "secret.denied" // local → node: why the key was refused
)

// secretGet is the body of a typeSecretGet message. Nonce is single use: local
// satelle refuses one it has seen, and the node accepts one answer per nonce.
type secretGet struct {
	Key   string `json:"key"`
	Nonce string `json:"nonce"`
}

// secretAnswer is the body of a typeSecretValue or typeSecretDenied message.
type secretAnswer struct {
	Key    string `json:"key"`
	Nonce  string `json:"nonce"`
	Value  string `json:"value,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// SecretDeniedError is local satelle's refusal of a key, with its reason.
type SecretDeniedError struct {
	Key    string
	Reason string
}

func (e *SecretDeniedError) Error() string {
	return fmt.Sprintf("secret %s refused: %s", e.Key, e.Reason)
}

// SecretFetch gets one secret for job: a Node's FetchSecret, or a stand-in.
type SecretFetch func(ctx context.Context, job, key string) (string, error)

type jobCtxKey struct{}

// JobID is the channel job a handler is serving, "" outside one.
func JobID(ctx context.Context) string {
	id, _ := ctx.Value(jobCtxKey{}).(string)
	return id
}

// secretWaiter is a request the node sent and has not had answered.
type secretWaiter struct {
	job, key string
	ch       chan secretAnswer
}

// FetchSecret asks local satelle for key inside job and waits for the answer.
// The job must be one this node is serving. A refusal is a *SecretDeniedError.
// Nothing is cached: every call is a new request with a new nonce.
func (n *Node) FetchSecret(ctx context.Context, job, key string) (string, error) {
	nonce, err := newNonce()
	if err != nil {
		return "", err
	}
	w := &secretWaiter{job: job, key: key, ch: make(chan secretAnswer, 1)}
	n.mu.Lock()
	sess := n.sess
	_, live := n.active[job]
	if sess != nil && live {
		n.waiters[nonce] = w
	}
	n.mu.Unlock()
	switch {
	case sess == nil:
		return "", errors.New("remotenode: the node is not connected")
	case !live:
		return "", fmt.Errorf("remotenode: no job %q is running on this node", job)
	}
	defer func() {
		n.mu.Lock()
		delete(n.waiters, nonce)
		n.mu.Unlock()
	}()

	body, err := encode(typeSecretGet, secretGet{Key: key, Nonce: nonce})
	if err != nil {
		return "", err
	}
	if err := sess.Send(ctx, job, body); err != nil {
		return "", err
	}
	select {
	case a := <-w.ch:
		if a.Reason != "" {
			return "", &SecretDeniedError{Key: key, Reason: a.Reason}
		}
		return a.Value, nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-sess.Done():
		return "", remotechan.ErrClosed
	}
}

// deliverSecret hands an answer to the request waiting on its nonce, once. An
// answer for an unknown or already answered nonce, another job's, or another
// key's is dropped: it reports whether the answer was accepted.
func (n *Node) deliverSecret(job string, env envelope) bool {
	var a secretAnswer
	if json.Unmarshal(env.Body, &a) != nil {
		return false
	}
	if env.Type == typeSecretDenied && a.Reason == "" {
		a.Reason = "refused"
	}
	n.mu.Lock()
	w, ok := n.waiters[a.Nonce]
	if ok && w.job == job && w.key == a.Key {
		delete(n.waiters, a.Nonce)
	} else {
		ok = false
	}
	n.mu.Unlock()
	if !ok {
		return false
	}
	w.ch <- a
	return true
}

// SoleJob is the one job the node is serving, which a request from the node's
// shell with no job named belongs to. Several running jobs are ambiguous.
func (n *Node) SoleJob() (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	switch len(n.active) {
	case 0:
		return "", errors.New("no job is running on this node")
	case 1:
		for id := range n.active {
			return id, nil
		}
	}
	return "", errors.New("several jobs are running on this node: name one with --job")
}

func newNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
