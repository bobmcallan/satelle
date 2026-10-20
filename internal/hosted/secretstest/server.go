// Package secretstest is an in-process stand-in for satelle-server's secrets
// routes, written to the contract documented in package hosted's client_secrets.go
// and nothing else: secrets are uploaded with a value and listed without one, a
// remote reads only a key it holds a grant on, only through a live run, and only
// once per (run, key, nonce). It counts every request so a test can prove a
// refusal happened without the server's help, and keeps an audit of every read.
// It is test support only.
package secretstest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
)

// Request is one request the server received, with the body as sent.
type Request struct {
	Method, Path, Bearer string
	Body                 string
}

// Read is one entry of the server's audit of secret reads.
type Read struct {
	Key, Remote, Run, Outcome, Reason string
}

type secret struct {
	value    string
	personal bool
	readers  map[string]bool
}

type run struct {
	remote, wakeKey string
	live            bool
	refreshes       int
}

// Server is the fake. Create it with New.
type Server struct {
	srv *httptest.Server

	mu       sync.Mutex
	secrets  map[string]*secret
	runs     map[string]*run
	used     map[string]bool
	requests []Request
	audit    []Read
	fireable map[string]bool
	seq      int
}

// New starts a server for the life of t.
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{secrets: map[string]*secret{}, runs: map[string]*run{}, used: map[string]bool{}, fireable: map[string]bool{}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

// URL is the server base URL.
func (s *Server) URL() string { return s.srv.URL }

// Requests is every request received so far, in order.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// Calls counts the requests received so far.
func (s *Server) Calls() int { return len(s.Requests()) }

// CallsTo counts the requests whose method and path start with the prefix, e.g.
// "POST /api/v1/remotes/".
func (s *Server) CallsTo(prefix string) int {
	n := 0
	for _, r := range s.Requests() {
		if strings.HasPrefix(r.Method+" "+r.Path, prefix) {
			n++
		}
	}
	return n
}

// Audit is every secret read, answered or refused.
func (s *Server) Audit() []Read {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Read(nil), s.audit...)
}

// Value is the value held for key, for a test to seed from and compare with.
func (s *Server) Value(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sec, ok := s.secrets[key]
	if !ok {
		return "", false
	}
	return sec.value, true
}

// MarkFireable says someone other than the creator can fire remote, so it may
// not read a personal key.
func (s *Server) MarkFireable(remote string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fireable[remote] = true
}

// Live reports whether run is registered and not ended or expired.
func (s *Server) Live(run string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[run]
	return ok && r.live
}

// Refreshes is how many times run was refreshed.
func (s *Server) Refreshes(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.runs[id]; ok {
		return r.refreshes
	}
	return 0
}

// Runs lists the ids of every registered run.
func (s *Server) Runs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for id := range s.runs {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Expire ends run as the server's backstop would, with no end call from a client.
func (s *Server) Expire(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.runs[id]; ok {
		r.live = false
	}
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.requests = append(s.requests, Request{Method: r.Method, Path: r.URL.Path, Bearer: r.Header.Get("Authorization"), Body: string(body)})
	s.mu.Unlock()

	segs := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case len(segs) >= 3 && segs[0] == "api" && segs[1] == "v1" && segs[2] == "secrets":
		s.secretsRoute(w, r, segs[3:], body)
	case len(segs) >= 5 && segs[2] == "remotes" && segs[4] == "runs":
		s.runsRoute(w, r, segs[3], segs[5:], body)
	default:
		http.NotFound(w, r)
	}
}

func reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func forbid(w http.ResponseWriter, reason string) {
	reply(w, http.StatusForbidden, map[string]string{"error": "forbidden", "reason": reason})
}

func (s *Server) secretsRoute(w http.ResponseWriter, r *http.Request, rest []string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		out := []map[string]any{}
		keys := make([]string, 0, len(s.secrets))
		for k := range s.secrets {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sec := s.secrets[k]
			readers := []string{}
			for rd := range sec.readers {
				readers = append(readers, rd)
			}
			sort.Strings(readers)
			out = append(out, map[string]any{"key": k, "has_value": sec.value != "", "personal": sec.personal, "readers": readers})
		}
		reply(w, http.StatusOK, out)
	case len(rest) == 1 && r.Method == http.MethodPut:
		var in struct {
			Value    string `json:"value"`
			Personal bool   `json:"personal"`
		}
		if json.Unmarshal(body, &in) != nil {
			reply(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
			return
		}
		sec, ok := s.secrets[rest[0]]
		if !ok {
			sec = &secret{readers: map[string]bool{}}
			s.secrets[rest[0]] = sec
		}
		sec.value, sec.personal = in.Value, in.Personal
		reply(w, http.StatusOK, nil)
	case len(rest) == 1 && r.Method == http.MethodDelete:
		delete(s.secrets, rest[0])
		reply(w, http.StatusOK, nil)
	case len(rest) == 3 && rest[1] == "grants":
		sec, ok := s.secrets[rest[0]]
		if !ok {
			reply(w, http.StatusNotFound, map[string]string{"error": "no_such_secret"})
			return
		}
		switch r.Method {
		case http.MethodPut:
			var in struct {
				Personal bool `json:"personal"`
			}
			_ = json.Unmarshal(body, &in)
			sec.readers[rest[2]] = true
			sec.personal = sec.personal || in.Personal
		case http.MethodDelete:
			delete(sec.readers, rest[2])
		}
		reply(w, http.StatusOK, nil)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) runsRoute(w http.ResponseWriter, r *http.Request, remote string, rest []string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case len(rest) == 0 && r.Method == http.MethodPost:
		var in struct {
			WakeKey string `json:"wake_key"`
		}
		_ = json.Unmarshal(body, &in)
		s.seq++
		id := fmt.Sprintf("run-%d", s.seq)
		s.runs[id] = &run{remote: remote, wakeKey: in.WakeKey, live: true}
		reply(w, http.StatusCreated, map[string]string{"run": id})
	case len(rest) == 1 && strings.HasSuffix(rest[0], ":refresh") && r.Method == http.MethodPost:
		id := strings.TrimSuffix(rest[0], ":refresh")
		ru, ok := s.runs[id]
		if !ok || !ru.live || ru.remote != remote {
			forbid(w, "the run is not live")
			return
		}
		ru.refreshes++
		reply(w, http.StatusOK, nil)
	case len(rest) == 1 && r.Method == http.MethodDelete:
		if ru, ok := s.runs[rest[0]]; ok && ru.remote == remote {
			ru.live = false
		}
		reply(w, http.StatusOK, nil)
	case len(rest) == 3 && rest[1] == "secrets" && strings.HasSuffix(rest[2], ":read") && r.Method == http.MethodPost:
		s.read(w, remote, rest[0], strings.TrimSuffix(rest[2], ":read"), body)
	default:
		http.NotFound(w, r)
	}
}

// read answers a secret read, auditing it. The caller holds s.mu.
func (s *Server) read(w http.ResponseWriter, remote, runID, key string, body []byte) {
	deny := func(reason string) {
		s.audit = append(s.audit, Read{Key: key, Remote: remote, Run: runID, Outcome: "denied", Reason: reason})
		forbid(w, reason)
	}
	var in struct {
		Nonce string `json:"nonce"`
	}
	_ = json.Unmarshal(body, &in)
	ru, ok := s.runs[runID]
	sec := s.secrets[key]
	switch {
	case !ok || !ru.live || ru.remote != remote:
		deny("the run is not live")
	case key == ru.wakeKey:
		deny("the key is readable only by the server's waker")
	case sec == nil || !sec.readers[remote]:
		deny("remote " + remote + " holds no grant on " + key)
	case sec.personal && s.fireable[remote]:
		deny("a personal key is not readable by a remote others can fire")
	case in.Nonce == "" || s.used[runID+"|"+key+"|"+in.Nonce]:
		deny("the request was already used")
	default:
		s.used[runID+"|"+key+"|"+in.Nonce] = true
		s.audit = append(s.audit, Read{Key: key, Remote: remote, Run: runID, Outcome: "allowed"})
		reply(w, http.StatusOK, map[string]string{"value": sec.value})
	}
}
