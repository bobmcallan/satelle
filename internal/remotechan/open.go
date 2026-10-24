package remotechan

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/coder/websocket"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
)

// ErrRelayRefused is returned when the relay answers the upgrade with a status
// other than 101 that signing in again would not fix.
var ErrRelayRefused = errors.New("remotechan: relay refused the connection")

// RelayStatusError is ErrRelayRefused with the status the relay answered the
// upgrade with, so a caller can tell a refused credential (401) from the rest.
type RelayStatusError struct{ Status int }

func (e *RelayStatusError) Error() string {
	return fmt.Sprintf("%s (HTTP %d)", ErrRelayRefused.Error(), e.Status)
}
func (e *RelayStatusError) Is(target error) bool { return target == ErrRelayRefused }

// ErrEnrollRefused is returned to a remote that local satelle refused.
var ErrEnrollRefused = errors.New("remotechan: enrollment refused")

// LaunchSpec is what local satelle hands the launcher to start a node with: the
// relay to dial, the session to join and the single-use token to enroll with.
type LaunchSpec struct {
	RelayURL string
	Session  string
	Token    string
}

// Launcher starts a node and returns the node's public key as the launch read
// it. local satelle pins that key; the key is read at launch, never from the
// channel, so a party in the middle cannot supply its own.
type Launcher interface {
	Launch(ctx context.Context, spec LaunchSpec) (nodePublicKey []byte, err error)
}

// LauncherFunc adapts a function to a Launcher.
type LauncherFunc func(ctx context.Context, spec LaunchSpec) ([]byte, error)

// Launch calls f.
func (f LauncherFunc) Launch(ctx context.Context, spec LaunchSpec) ([]byte, error) {
	return f(ctx, spec)
}

// Recorder is where refused enrollments are written: the evidence ledger's
// Append, which *ledger.Store satisfies.
type Recorder interface {
	Append(ctx context.Context, in ledger.AppendInput, now time.Time) (ledger.Entry, error)
}

type options struct {
	http   *http.Client
	clock  Clock
	tokens *TokenBook
	rec    Recorder
}

// Option configures Open or Join.
type Option func(*options)

// WithHTTPClient sets the client the relay dial uses (default: http.DefaultClient,
// which honours HTTPS_PROXY). The client must have no Timeout: a WebSocket is long-lived.
func WithHTTPClient(c *http.Client) Option { return func(o *options) { o.http = c } }

// WithClock sets the time source (default: the real clock).
func WithClock(c Clock) Option { return func(o *options) { o.clock = c } }

// WithTokens sets the token book Open mints from and redeems against (default: a
// fresh one per Open).
func WithTokens(b *TokenBook) Option { return func(o *options) { o.tokens = b } }

// WithRecorder sets where refused enrollments are recorded (default: nowhere).
func WithRecorder(r Recorder) Option { return func(o *options) { o.rec = r } }

func newOptions(opts []Option) *options {
	o := &options{}
	for _, f := range opts {
		f(o)
	}
	if o.clock == nil {
		o.clock = RealClock()
	}
	if o.tokens == nil {
		o.tokens = NewTokenBook(o.clock)
	}
	return o
}

// Open is local satelle's end. It dials the relay with the satelle.dev bearer
// (a signed-out client returns hosted.ErrLoginRequired before anything is
// minted or launched), then mints a single-use token, launches the node, pins
// the key the launch returned, and waits for the node to enroll. The returned
// Session is admitted and sealed. It lives until Close.
//
// Only dialing out: Open never listens.
func Open(ctx context.Context, client *hosted.Client, relayURL string, launch Launcher, opts ...Option) (*Session, error) {
	o := newOptions(opts)
	session, err := newSessionID()
	if err != nil {
		return nil, err
	}
	conn, err := dialLocal(ctx, client, o, relayURL, session)
	if err != nil {
		return nil, err
	}

	token, err := o.tokens.Mint()
	if err == nil {
		var pinned []byte
		if pinned, err = launch.Launch(ctx, LaunchSpec{RelayURL: relayURL, Session: session, Token: token}); err == nil {
			var s *Session
			redeem := func(m appMsg) error { return o.tokens.Redeem(m.Token) }
			if s, err = acceptRemote(ctx, conn, session, pinned, o, redeem); err == nil {
				return s, nil
			}
		}
	}
	conn.Close(websocket.StatusPolicyViolation, "open failed")
	return nil, err
}

// dialLocal dials the relay as local with the satelle.dev bearer, refreshing it
// once on a 401. It is the one local dial, shared by Open and AttachServerSession.
func dialLocal(ctx context.Context, client *hosted.Client, o *options, relayURL, session string) (*websocket.Conn, error) {
	var conn *websocket.Conn
	var status int
	err := client.WithBearer(ctx, func(ctx context.Context, bearer string) (int, []byte, error) {
		c, resp, err := dialRelay(ctx, o.http, relayURL, session, RoleLocal, bearer)
		if err != nil {
			if resp != nil {
				status = resp.StatusCode
				return status, hosted.BoundAuthBody(resp.Body), nil
			}
			return 0, nil, fmt.Errorf("remotechan: dial relay: %w", err)
		}
		conn, status = c, http.StatusSwitchingProtocols
		return status, nil, nil
	})
	if err != nil {
		return nil, err
	}
	if conn == nil {
		return nil, &RelayStatusError{Status: status}
	}
	return conn, nil
}

// ErrKeyUnpublished is returned by AttachServerSession when the node never
// published its channel key within the bound, so there is nothing to pin.
var ErrKeyUnpublished = errors.New("remotechan: the node published no channel key")

// keyPollInterval is how often AttachServerSession asks for the node's key while
// it is not yet published.
const keyPollInterval = time.Second

// AttachServerSession is local's end of a session satelle.dev minted: the
// session id is the node id the server started, and the relay has already
// authenticated both ends (the starter as local, the node by its node token).
// There is no local mint and no enroll token — TokenBook is never consulted.
//
// Admission is by key. keyFn reads the key the node published (nil until it
// does); it is asked until a key is present, for at most EnrollTTL on the
// options clock, and a node that never publishes one is refused
// (ErrKeyUnpublished, recorded as key_unpublished) with the session closed. The
// handshake then runs pinned to that key: a node holding any other key cannot
// derive the session keys and is refused as key_mismatch. No frame is trusted
// before the key is present and matches, and there is no trust on first use.
//
// The node side is unchanged: Join sends its bearer as the enroll value, which
// this path ignores.
func AttachServerSession(ctx context.Context, client *hosted.Client, relayURL, session string, keyFn func(context.Context) ([]byte, error), opts ...Option) (*Session, error) {
	o := newOptions(opts)
	conn, err := dialLocal(ctx, client, o, relayURL, session)
	if err != nil {
		return nil, err
	}
	pinned, err := awaitKey(ctx, o, keyFn)
	if err != nil {
		if errors.Is(err, ErrKeyUnpublished) {
			err = o.refused(ctx, session, "key_unpublished", err)
		}
		conn.Close(websocket.StatusPolicyViolation, "attach failed")
		return nil, err
	}
	admitByKey := func(appMsg) error { return nil } // the pinned key already authenticated the node
	s, err := acceptRemote(ctx, conn, session, pinned, o, admitByKey)
	if err != nil {
		conn.Close(websocket.StatusPolicyViolation, "attach failed")
		return nil, err
	}
	return s, nil
}

// awaitKey asks keyFn for the node's published key until one is present.
func awaitKey(ctx context.Context, o *options, keyFn func(context.Context) ([]byte, error)) ([]byte, error) {
	deadline := o.clock.Now().Add(EnrollTTL)
	tick, stop := o.clock.Tick(keyPollInterval)
	defer stop()
	for {
		key, err := keyFn(ctx)
		if err != nil {
			return nil, fmt.Errorf("remotechan: reading the node's published key: %w", err)
		}
		if len(key) > 0 {
			return key, nil
		}
		if !o.clock.Now().Before(deadline) {
			return nil, ErrKeyUnpublished
		}
		select {
		case <-tick:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// acceptRemote runs local's half of the handshake on an already-dialed
// connection: read the remote's key half, answer with ours, derive the keys
// against the pinned node key, open the remote's enrollment, then ask admit
// whether to let it in. Open admits by redeeming the enrollment's single-use
// token; AttachServerSession admits by the key the keys were already derived
// from. The handshake, the key_mismatch refusal and the refusal recorder are the
// same for both.
func acceptRemote(ctx context.Context, conn *websocket.Conn, session string, pinned []byte, o *options, admit func(appMsg) error) (*Session, error) {
	conn.SetReadLimit(maxFrameBytes)
	hctx, cancel := context.WithTimeout(ctx, EnrollTTL)
	defer cancel()

	frame, err := readFrame(hctx, conn)
	if err != nil {
		return nil, fmt.Errorf("remotechan: waiting for the remote: %w", err)
	}
	if len(frame) != 1+pubLen || frame[0] != frameHelloRemote {
		return nil, fmt.Errorf("%w: expected the remote's key half", ErrFrame)
	}
	eL, err := newEphemeral()
	if err != nil {
		return nil, err
	}
	send, recv, err := localKeys(session, eL, frame[1:], pinned)
	if err != nil {
		return nil, err
	}
	if err := conn.Write(hctx, websocket.MessageBinary, append([]byte{frameHelloLocal}, eL.PublicKey().Bytes()...)); err != nil {
		return nil, err
	}

	first, err := readFrame(hctx, conn)
	if err != nil {
		return nil, fmt.Errorf("remotechan: waiting for enrollment: %w", err)
	}
	plain, err := recv.open(first)
	if errors.Is(err, errOpen) {
		return nil, o.refused(ctx, session, "key_mismatch", ErrKeyMismatch)
	}
	if err != nil {
		return nil, err
	}
	var m appMsg
	if json.Unmarshal(plain, &m) != nil || m.T != msgEnroll {
		return nil, fmt.Errorf("%w: expected enrollment", ErrFrame)
	}

	if rErr := admit(m); rErr != nil {
		reply, _ := json.Marshal(appMsg{T: msgRefuse, Reason: rErr.Error()})
		_ = conn.Write(hctx, websocket.MessageBinary, send.seal(reply))
		return nil, o.refused(ctx, session, tokenReason(rErr), rErr)
	}
	admitted, _ := json.Marshal(appMsg{T: msgAdmit})
	if err := conn.Write(hctx, websocket.MessageBinary, send.seal(admitted)); err != nil {
		return nil, err
	}
	return newSession(ctx, RoleLocal, conn, send, recv, o.clock), nil
}

func tokenReason(err error) string {
	switch {
	case errors.Is(err, ErrTokenUsed):
		return "token_used"
	case errors.Is(err, ErrTokenExpired):
		return "token_expired"
	default:
		return "token_unknown"
	}
}

// refused records one refused enrollment and returns cause, joined with the
// recording failure if the ledger could not be written.
func (o *options) refused(ctx context.Context, session, reason string, cause error) error {
	if o.rec == nil {
		return cause
	}
	payload, _ := json.Marshal(map[string]string{"session": session, "reason": reason})
	_, err := o.rec.Append(ctx, ledger.AppendInput{
		Kind: ledger.KindRemoteEnrollRefused, Actor: "remotechan", Body: reason, Payload: payload,
	}, o.clock.Now())
	if err != nil {
		return errors.Join(cause, fmt.Errorf("remotechan: record refused enrollment: %w", err))
	}
	return cause
}

// Join is the remote's end: dial the relay out with the node token as the
// bearer, complete the handshake with the node's own identity, and enroll with
// the same token. It returns an admitted Session, ErrEnrollRefused if local
// satelle refused the token, a *RelayStatusError (401 for a token the relay does
// not accept) if the relay refused the upgrade, or an error if the channel closed
// first (as it does for a key local did not pin).
func Join(ctx context.Context, relayURL, session string, id *Identity, token string, opts ...Option) (*Session, error) {
	o := newOptions(opts)
	conn, resp, err := dialRelay(ctx, o.http, relayURL, session, RoleRemote, token)
	if err != nil {
		if resp != nil {
			hosted.BoundAuthBody(resp.Body)
			return nil, &RelayStatusError{Status: resp.StatusCode}
		}
		return nil, fmt.Errorf("remotechan: dial relay: %w", err)
	}
	s, err := joinHandshake(ctx, conn, session, id, token, o)
	if err != nil {
		conn.Close(websocket.StatusPolicyViolation, "join failed")
		return nil, err
	}
	return s, nil
}

func joinHandshake(ctx context.Context, conn *websocket.Conn, session string, id *Identity, token string, o *options) (*Session, error) {
	conn.SetReadLimit(maxFrameBytes)
	hctx, cancel := context.WithTimeout(ctx, EnrollTTL)
	defer cancel()

	eR, err := newEphemeral()
	if err != nil {
		return nil, err
	}
	if err := conn.Write(hctx, websocket.MessageBinary, append([]byte{frameHelloRemote}, eR.PublicKey().Bytes()...)); err != nil {
		return nil, err
	}
	frame, err := readFrame(hctx, conn)
	if err != nil {
		return nil, fmt.Errorf("remotechan: waiting for local satelle: %w", err)
	}
	if len(frame) != 1+pubLen || frame[0] != frameHelloLocal {
		return nil, fmt.Errorf("%w: expected local's key half", ErrFrame)
	}
	send, recv, err := remoteKeys(session, id, eR, frame[1:])
	if err != nil {
		return nil, err
	}
	enroll, _ := json.Marshal(appMsg{T: msgEnroll, Token: token})
	if err := conn.Write(hctx, websocket.MessageBinary, send.seal(enroll)); err != nil {
		return nil, err
	}

	reply, err := readFrame(hctx, conn)
	if err != nil {
		return nil, fmt.Errorf("remotechan: channel closed before admission: %w", err)
	}
	plain, err := recv.open(reply)
	if err != nil {
		return nil, err
	}
	var m appMsg
	if json.Unmarshal(plain, &m) != nil {
		return nil, fmt.Errorf("%w: malformed reply", ErrFrame)
	}
	switch m.T {
	case msgAdmit:
		return newSession(ctx, RoleRemote, conn, send, recv, o.clock), nil
	case msgRefuse:
		return nil, fmt.Errorf("%w: %s", ErrEnrollRefused, m.Reason)
	}
	return nil, fmt.Errorf("%w: expected admission", ErrFrame)
}

// dialRelay opens the WebSocket to <relayURL>/<session>?role=<role>. A bearer
// ("" for none) rides in the upgrade request. When the relay answers the
// upgrade with a non-101 status the response is returned beside the error.
func dialRelay(ctx context.Context, httpc *http.Client, relayURL, session string, role Role, bearer string) (*websocket.Conn, *http.Response, error) {
	u, err := url.JoinPath(relayURL, session)
	if err != nil {
		return nil, nil, err
	}
	d := &websocket.DialOptions{HTTPClient: httpc}
	if bearer != "" {
		d.HTTPHeader = http.Header{"Authorization": {"Bearer " + bearer}}
	}
	return websocket.Dial(ctx, u+"?role="+role.String(), d)
}

func readFrame(ctx context.Context, conn *websocket.Conn) ([]byte, error) {
	typ, data, err := conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	if typ != websocket.MessageBinary {
		return nil, fmt.Errorf("%w: text message on the channel", ErrFrame)
	}
	return data, nil
}

// newSessionID is the 128-bit random name the relay pairs the two ends under.
func newSessionID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("remotechan: session id: %w", err)
	}
	return hex.EncodeToString(b), nil
}
