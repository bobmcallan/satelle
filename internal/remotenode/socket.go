package remotenode

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// The node's agent runs in a process of its own, so `satelle secret get` reaches
// the join process that holds the session over a unix socket in the node's data
// dir. The socket sits in a directory only the node's user can enter and is
// itself mode 0600; it exists only while the join process runs. One line of JSON
// goes in and one comes out. Nothing is written to disk.

// socketDir and socketName place the socket under the node's data dir.
const (
	socketDir  = "run"
	socketName = "node.sock"
	// maxSocketPath is the shortest sun_path limit among the platforms we run on.
	maxSocketPath = 100
	// socketFetchTimeout bounds one secret request from the node's shell.
	socketFetchTimeout = 60 * time.Second
)

// SocketPath is where the node serves its secret socket: under dataDir, or, when
// that path is too long for a unix socket, under a private directory in the
// temp dir named by a hash of dataDir. The join process and the agent's shell
// both call it with the same data dir (symlinks resolved) and so agree.
func SocketPath(dataDir string) string {
	if real, err := filepath.EvalSymlinks(dataDir); err == nil {
		dataDir = real
	}
	p := filepath.Join(dataDir, socketDir, socketName)
	if len(p) <= maxSocketPath {
		return p
	}
	sum := sha256.Sum256([]byte(dataDir))
	return filepath.Join(os.TempDir(), "satelle-"+hex.EncodeToString(sum[:6]), socketName)
}

// privateDir makes dir (and its parents) and checks it is a real directory this
// user owns, closed to everyone else: chmod fails on a directory someone else
// created first, and a symlink in its place is refused.
func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if st, err := os.Lstat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("remotenode: %s is not a directory", dir)
	}
	return os.Chmod(dir, 0o700)
}

type socketRequest struct {
	Key string `json:"key"`
	Job string `json:"job,omitempty"`
}

type socketResponse struct {
	Value string `json:"value,omitempty"`
	Error string `json:"error,omitempty"`
}

// ServeSocket serves secret requests from the node's shell at path until ctx ends
// or the returned close func is called. A request with no job named belongs to
// the node's one running job.
func (n *Node) ServeSocket(ctx context.Context, path string) (func(), error) {
	if len(path) > maxSocketPath {
		return nil, fmt.Errorf("remotenode: socket path %q is longer than %d bytes", path, maxSocketPath)
	}
	if err := privateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	_ = os.Remove(path) // a stale socket from a join that did not exit cleanly
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("remotenode: listen on %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go n.serveSocketConn(ctx, conn)
		}
	}()
	closed := make(chan struct{})
	closeFn := func() {
		select {
		case <-closed:
			return
		default:
			close(closed)
		}
		ln.Close()
		_ = os.Remove(path)
	}
	go func() {
		select {
		case <-ctx.Done():
			closeFn()
		case <-closed:
		}
	}()
	return closeFn, nil
}

func (n *Node) serveSocketConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(socketFetchTimeout + 5*time.Second))
	var req socketRequest
	line, err := bufio.NewReaderSize(conn, 4096).ReadBytes('\n')
	if err == nil {
		err = json.Unmarshal(line, &req)
	}
	var resp socketResponse
	switch {
	case err != nil || req.Key == "":
		resp.Error = "malformed request"
	default:
		job := req.Job
		if job == "" {
			job, err = n.SoleJob()
		}
		if err == nil {
			fctx, cancel := context.WithTimeout(ctx, socketFetchTimeout)
			resp.Value, err = n.FetchSecret(fctx, job, req.Key)
			cancel()
		}
		if err != nil {
			resp.Error = err.Error()
		}
	}
	_ = json.NewEncoder(conn).Encode(resp)
}

// SocketGet asks the join process serving path for key. job names the job when
// the node runs several. A refusal comes back as an error carrying the reason.
func SocketGet(ctx context.Context, path, key, job string) (string, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return "", fmt.Errorf("no remote node is serving secrets here (%s): satelle secret get runs on a remote node, under its agent: %w", path, err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	req, _ := json.Marshal(socketRequest{Key: key, Job: job})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return "", err
	}
	var resp socketResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return "", fmt.Errorf("the node did not answer: %w", err)
	}
	if resp.Error != "" {
		return "", errors.New(resp.Error)
	}
	return resp.Value, nil
}
