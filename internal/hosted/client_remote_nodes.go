package hosted

// The starter's calls to start a performer node of a published remote and to read
// the channel key that node published (sty_7028d996). The server starts the node
// and mints its session; the session id is the node id. Both calls go on the CLI
// token. Publishing the key is the node's call (remotenode.NodeClient).

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// PerformerNodesDependency names the satelle-server story that ships the nodes
// route; until it does the server answers 404 and the starter says so.
const PerformerNodesDependency = "satelle-server#sty_e272c46f"

// PerformerNode is a started performer node: its id (== the relay session) and
// the relay address both ends dial.
type PerformerNode struct {
	NodeID   string `json:"node_id"`
	RelayURL string `json:"relay_url"`
}

// StartPerformerNode asks the server to start a performer-mode node of remoteID
// for storyID, to perform binding (POST /api/v1/remotes/{id}/nodes). 201 carries
// the node; any other answer is an error with the server's reason. A 404 names
// the dependency that has not shipped.
func (c *Client) StartPerformerNode(ctx context.Context, remoteID, storyID, binding string) (nodeID, relayURL string, err error) {
	payload, err := json.Marshal(map[string]string{"mode": "performer", "story_id": storyID, "binding": binding})
	if err != nil {
		return "", "", err
	}
	resp, err := c.doAuthed(ctx, http.MethodPost, remoteRoute(remoteID)+"/nodes", payload, "application/json")
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxCallBody))
	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK:
		var n PerformerNode
		if err := json.Unmarshal(body, &n); err != nil || n.NodeID == "" || n.RelayURL == "" {
			return "", "", fmt.Errorf("hosted: start performer node of %s: the answer names no node and relay", remoteID)
		}
		return n.NodeID, n.RelayURL, nil
	case http.StatusUnauthorized:
		return "", "", ErrLoginRequired
	case http.StatusNotFound:
		return "", "", fmt.Errorf("hosted: start performer node of %s: the server has no performer-nodes route yet (waiting on %s): %w",
			remoteID, PerformerNodesDependency, newAPIError(resp.StatusCode, body))
	default:
		return "", "", fmt.Errorf("hosted: start performer node of %s: %w", remoteID, newAPIError(resp.StatusCode, body))
	}
}

// GetRemoteNode reads the channel key node nodeID published (GET
// /api/v1/remote-nodes/{id}, starter-only). The key is nil, with no error, until
// the node has published it.
func (c *Client) GetRemoteNode(ctx context.Context, nodeID string) (publicKey []byte, err error) {
	var out struct {
		PublicKey string `json:"public_key"`
	}
	if err := c.getJSON(ctx, "/api/v1/remote-nodes/"+url.PathEscape(nodeID), &out); err != nil {
		return nil, err
	}
	if out.PublicKey == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(out.PublicKey)
	if err != nil {
		return nil, errors.New("hosted: remote node: the published public_key is not standard base64")
	}
	return key, nil
}
