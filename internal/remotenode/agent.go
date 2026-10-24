package remotenode

import "encoding/json"

// AgentJob is the job type that talks to the node's agent: the request carries
// the developer's message and the reply the agent's answer.
const AgentJob = "agent"

// AgentRequest is the body of an AgentJob request.
//
// Binding and SystemPrompt carry a performed step (sty_7028d996): Binding names
// the node's own agents.toml binding that performs it, SystemPrompt is the step's
// composed system prompt and Text the step's payload. Both are empty for a plain
// message, which the node answers with its default binding and prompt as before.
type AgentRequest struct {
	Text         string `json:"text"`
	Binding      string `json:"binding,omitempty"`
	SystemPrompt string `json:"system_prompt,omitempty"`
}

// AgentReply is the body of an AgentJob reply.
type AgentReply struct {
	Text string `json:"text"`
}

// DecodeAgentReply reads an AgentJob reply body.
func DecodeAgentReply(body json.RawMessage) (AgentReply, error) {
	var r AgentReply
	err := json.Unmarshal(body, &r)
	return r, err
}
