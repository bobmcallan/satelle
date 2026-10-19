package remotenode

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/logsread"
)

// StoryReportJob is the job type that asks the node what happened around a story
// it was just run: where the story's sessions were logged and whether its own
// configuration changed.
const StoryReportJob = "story_report"

// StoryReportRequest is the body of a StoryReportJob request: the story, and the
// node-clock time (Now of an earlier reply or of the join report) from which its
// sessions count.
type StoryReportRequest struct {
	Story string `json:"story"`
	Since int64  `json:"since"`
}

// StoryReportReply is the body of a StoryReportJob reply. Now is the node's own
// clock in unix nanoseconds — the only clock a Since is ever taken from. Sessions
// are the node-side dispatch-log paths of the story at or after Since. Hashes is
// config.ProcessHashes of the node's process.
type StoryReportReply struct {
	Now      int64             `json:"now"`
	Sessions []string          `json:"sessions"`
	Hashes   map[string]string `json:"hashes"`
}

// NodeStoryReport answers one StoryReportRequest for the node whose working root
// is root and whose process config is process.
func NodeStoryReport(process config.Config, root string, req StoryReportRequest, now time.Time) (StoryReportReply, error) {
	files, err := logsread.ListDispatch(process.ResolveLogsDir(root))
	if err != nil {
		return StoryReportReply{}, fmt.Errorf("listing dispatch logs: %w", err)
	}
	rep := StoryReportReply{Now: now.UnixNano(), Sessions: []string{}, Hashes: config.ProcessHashes(process, root)}
	for _, f := range files {
		if f.Story == req.Story && f.Nano >= req.Since {
			rep.Sessions = append(rep.Sessions, f.Path)
		}
	}
	return rep, nil
}

// StoryReportHandler serves StoryReportJob from the node's process config and
// working root; load returns them.
func StoryReportHandler(load func() (config.Config, string, error)) Handler {
	return func(_ context.Context, body json.RawMessage) (json.RawMessage, error) {
		var req StoryReportRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		process, root, err := load()
		if err != nil {
			return nil, err
		}
		rep, err := NodeStoryReport(process, root, req, time.Now())
		if err != nil {
			return nil, err
		}
		return json.Marshal(rep)
	}
}

// StoryReport asks the node for the report on story, counting sessions from since
// (a node-clock Now). The reply's Now is the since to ask next.
func (r *Run) StoryReport(ctx context.Context, story string, since int64) (StoryReportReply, error) {
	body, err := r.Request(ctx, StoryReportJob, StoryReportRequest{Story: story, Since: since})
	if err != nil {
		return StoryReportReply{}, err
	}
	var rep StoryReportReply
	if err := json.Unmarshal(body, &rep); err != nil {
		return StoryReportReply{}, fmt.Errorf("remotenode: malformed story report: %w", err)
	}
	return rep, nil
}
