// Package syncscope resolves a repo's sync scope from the signed-in account and
// the repo's explicit [sync] project binding — the one owner of that decision.
// The repo holds only the binding; the account decides what it means:
//
//   - unbound or not signed in: local, with no hosted call;
//   - bound to a project of the account's personal team: personal;
//   - bound to a project of a team the account is a member of: team;
//   - bound to a project the account cannot see, or whose team it is not in: local.
//
// Any other hosted error is returned, never read as local: a sync must not
// silently stop syncing on a network fault.
package syncscope

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
)

// Account is the hosted surface the resolver reads; *hosted.Client satisfies it.
type Account interface {
	Me(ctx context.Context) (hosted.Principal, error)
	GetProject(ctx context.Context, project string) (hosted.Project, error)
	Workspaces(ctx context.Context) ([]hosted.Workspace, error)
}

// Input is what a resolve reads. Binding is the explicit [sync] project
// (config.Config.SyncProject), never the directory-name default.
type Input struct {
	Binding  string
	SignedIn bool
	Account  Account
}

// Team is the team that owns the bound project, as the account sees it.
type Team struct {
	ID   string
	Name string
	Kind string
}

// Resolution is a resolved scope and where it came from.
type Resolution struct {
	Scope   config.Scope
	Account string // the signed-in account's email, when the account was asked
	Team    Team
	Project string // the bound project slug, "" when unbound
	// Reason says why a local scope is local; empty otherwise.
	Reason string
}

// Local reasons that need no account call.
const (
	ReasonUnbound   = "no project bound — run `satelle project bind <slug>`"
	ReasonSignedOut = "not signed in — run `satelle login`"
)

// Resolve applies the scope rule. It calls the account only when the repo is
// bound and the machine is signed in.
func Resolve(ctx context.Context, in Input) (Resolution, error) {
	binding := strings.TrimSpace(in.Binding)
	res := Resolution{Scope: config.LocalScope, Project: binding}
	if binding == "" {
		res.Reason = ReasonUnbound
		return res, nil
	}
	if !in.SignedIn || in.Account == nil {
		res.Reason = ReasonSignedOut
		return res, nil
	}
	me, err := in.Account.Me(ctx)
	if err != nil {
		return res, fmt.Errorf("sync scope: who am I: %w", err)
	}
	res.Account = accountName(me)
	proj, err := in.Account.GetProject(ctx, binding)
	if errors.Is(err, hosted.ErrProjectNotFound) {
		res.Reason = fmt.Sprintf("project %s is not visible to %s", binding, res.Account)
		return res, nil
	}
	if err != nil {
		return res, fmt.Errorf("sync scope: project %s: %w", binding, err)
	}
	if proj.Slug != "" {
		res.Project = proj.Slug
	}
	workspaces, err := in.Account.Workspaces(ctx)
	if err != nil {
		return res, fmt.Errorf("sync scope: teams of %s: %w", res.Account, err)
	}
	for _, w := range workspaces {
		if proj.TeamID == "" || w.ID != proj.TeamID {
			continue
		}
		res.Team = Team{ID: w.ID, Name: w.Name, Kind: w.Kind}
		switch w.Kind {
		case "personal":
			res.Scope = config.PersonalScope
		case "team":
			res.Scope = config.TeamScope
		default:
			res.Reason = fmt.Sprintf("team %s of project %s has unknown kind %q", w.Name, res.Project, w.Kind)
		}
		return res, nil
	}
	res.Reason = fmt.Sprintf("%s is not a member of project %s's team", res.Account, res.Project)
	return res, nil
}

// Line renders a resolution as the one scope line every sync surface prints.
func (r Resolution) Line() string {
	switch r.Scope {
	case config.TeamScope:
		return fmt.Sprintf("scope: team — account %s, team %s (%s), project %s", r.Account, r.Team.Name, r.Team.ID, r.Project)
	case config.PersonalScope:
		return fmt.Sprintf("scope: personal — account %s, team %s (personal), project %s", r.Account, r.Team.Name, r.Project)
	default:
		return "scope: local — " + r.Reason
	}
}

func accountName(p hosted.Principal) string {
	for _, s := range []string{p.Email, p.DisplayName, p.ID} {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return "the signed-in account"
}
