// Package remotenode runs a remote node end to end. Local satelle starts it from
// a remote definition with the definition's own host commands, enrolls it on the
// sealed channel (package remotechan, used unchanged), ships it the definition's
// substrate, and asks it to report ready. The node side receives that setup,
// fetches the declared repos read-only, and serves jobs.
//
// The engine knows no host vendor: every host command is the user's own argv
// from the definition, and the package only fills its placeholders, runs it under
// package procgroup and records each start and stop in the evidence ledger.
//
// Everything that crosses the channel is a JSON envelope inside a sealed
// remotechan message. Setup and ready travel outside any job; a request is a job
// named by a type ("agent" here, any other a later story registers) and its reply
// is the same job's message, with the node heartbeating while it works.
package remotenode

import (
	"encoding/json"
	"fmt"
)

// Envelope types for the messages outside a job.
const (
	typeSetup = "setup" // local → node: the substrate, allowlist and repos
	typeReady = "ready" // node → local: the report, or why it cannot be ready
	typeReply = "reply" // node → local: a job's result
)

// envelope is what a remotechan message body carries.
type envelope struct {
	Type string          `json:"type"`
	Body json.RawMessage `json:"body,omitempty"`
}

func encode(typ string, body any) ([]byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope{Type: typ, Body: raw})
}

func decode(b []byte) (envelope, error) {
	var e envelope
	if err := json.Unmarshal(b, &e); err != nil {
		return envelope{}, fmt.Errorf("remotenode: malformed message: %w", err)
	}
	if e.Type == "" {
		return envelope{}, fmt.Errorf("remotenode: message has no type")
	}
	return e, nil
}

// RepoSpec is one repository the node is asked to fetch.
type RepoSpec struct {
	URL string `json:"url"`
	Ref string `json:"ref"`
}

// Setup is everything local satelle sends a node once it is admitted. It holds
// no credential: the substrate is the remote's workflows, skills and principles,
// and Allow is the egress local satelle applied to the host.
type Setup struct {
	// Substrate is the gzip-compressed tar of the remote's workflows, skills and
	// principles (see PackSubstrate).
	Substrate []byte     `json:"substrate"`
	Allow     []string   `json:"allow"`
	Repos     []RepoSpec `json:"repos"`
}

// FetchedRepo is one repo as the node fetched it.
type FetchedRepo struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Ref    string `json:"ref"`
	Commit string `json:"commit"`
}

// Report is what a node says about itself once it is ready to run the
// definition's workflows.
type Report struct {
	// Version is the satelle the node runs.
	Version string `json:"version"`
	// Workflows are the categories of the remote's own done.toml.
	Workflows []string `json:"workflows"`
	// Allow is the egress allowlist local satelle applied to the host.
	Allow []string      `json:"allow"`
	Repos []FetchedRepo `json:"repos"`
	// Bindings are the agents.toml bindings that resolved on the node.
	Bindings []string `json:"bindings"`
	// Now is the node's clock (unix nanoseconds) when it reported, and Hashes is
	// config.ProcessHashes of its process then, after the preflight: the baseline
	// a StoryReportJob is later compared with.
	Now    int64             `json:"now"`
	Hashes map[string]string `json:"hashes,omitempty"`
	// Error, when set, is why the node could not become ready; the other fields
	// are then unreliable.
	Error string `json:"error,omitempty"`
}

// reply is a job's result: Body on success, Error on failure.
type reply struct {
	Body  json.RawMessage `json:"body,omitempty"`
	Error string          `json:"error,omitempty"`
}
