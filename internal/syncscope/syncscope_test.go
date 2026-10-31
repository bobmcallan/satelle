package syncscope

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
)

// fakeAccount is the account service: projects by slug, the caller's
// workspaces, and a count of every call made.
type fakeAccount struct {
	projects   map[string]hosted.Project
	workspaces []hosted.Workspace
	projectErr error
	calls      int
}

func (f *fakeAccount) Me(context.Context) (hosted.Principal, error) {
	f.calls++
	return hosted.Principal{ID: "u1", Email: "ada@example.com"}, nil
}

func (f *fakeAccount) GetProject(_ context.Context, slug string) (hosted.Project, error) {
	f.calls++
	if f.projectErr != nil {
		return hosted.Project{}, f.projectErr
	}
	p, ok := f.projects[slug]
	if !ok {
		return hosted.Project{}, hosted.ErrProjectNotFound
	}
	return p, nil
}

func (f *fakeAccount) Workspaces(context.Context) ([]hosted.Workspace, error) {
	f.calls++
	return f.workspaces, nil
}

func newFake() *fakeAccount {
	return &fakeAccount{
		projects: map[string]hosted.Project{
			"solo":    {Slug: "solo", TeamID: "w-personal"},
			"acme":    {Slug: "acme", TeamID: "w-acme"},
			"foreign": {Slug: "foreign", TeamID: "w-other"},
		},
		workspaces: []hosted.Workspace{
			{ID: "w-personal", Kind: "personal", Name: "Ada"},
			{ID: "w-acme", Kind: "team", Name: "Acme"},
		},
	}
}

// AC2: the scope follows the account and the explicit binding alone.
func TestResolve(t *testing.T) {
	cases := []struct {
		name      string
		binding   string
		signedIn  bool
		want      config.Scope
		wantCalls int
		reason    string
		team      string
	}{
		{name: "unbound", binding: "", signedIn: true, want: config.LocalScope, wantCalls: 0, reason: "no project bound"},
		{name: "signed out", binding: "acme", signedIn: false, want: config.LocalScope, wantCalls: 0, reason: "not signed in"},
		{name: "personal team", binding: "solo", signedIn: true, want: config.PersonalScope, wantCalls: 3, team: "Ada"},
		{name: "member team", binding: "acme", signedIn: true, want: config.TeamScope, wantCalls: 3, team: "Acme"},
		{name: "not a member", binding: "foreign", signedIn: true, want: config.LocalScope, wantCalls: 3, reason: "ada@example.com is not a member of project foreign's team"},
		{name: "not visible", binding: "ghost", signedIn: true, want: config.LocalScope, wantCalls: 2, reason: "project ghost is not visible to ada@example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			res, err := Resolve(context.Background(), Input{Binding: tc.binding, SignedIn: tc.signedIn, Account: f})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if res.Scope != tc.want {
				t.Errorf("scope = %s, want %s (%+v)", res.Scope, tc.want, res)
			}
			if f.calls != tc.wantCalls {
				t.Errorf("account calls = %d, want %d", f.calls, tc.wantCalls)
			}
			if tc.reason != "" && !strings.Contains(res.Reason, tc.reason) {
				t.Errorf("reason = %q, want it to contain %q", res.Reason, tc.reason)
			}
			if tc.reason == "" && res.Reason != "" {
				t.Errorf("a non-local scope carries no reason, got %q", res.Reason)
			}
			if res.Team.Name != tc.team {
				t.Errorf("team = %q, want %q", res.Team.Name, tc.team)
			}
		})
	}
}

// A hosted fault is an error, never a silent local.
func TestResolveHostedErrorIsAnError(t *testing.T) {
	f := newFake()
	f.projectErr = errors.New("connection refused")
	res, err := Resolve(context.Background(), Input{Binding: "acme", SignedIn: true, Account: f})
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("err = %v, want the hosted fault (res %+v)", err, res)
	}
}

// The rendered line names the scope and its source.
func TestResolutionLine(t *testing.T) {
	f := newFake()
	team, _ := Resolve(context.Background(), Input{Binding: "acme", SignedIn: true, Account: f})
	if got, want := team.Line(), "scope: team — account ada@example.com, team Acme (w-acme), project acme"; got != want {
		t.Errorf("team line = %q, want %q", got, want)
	}
	personal, _ := Resolve(context.Background(), Input{Binding: "solo", SignedIn: true, Account: f})
	if got, want := personal.Line(), "scope: personal — account ada@example.com, team Ada (personal), project solo"; got != want {
		t.Errorf("personal line = %q, want %q", got, want)
	}
	local, _ := Resolve(context.Background(), Input{})
	if got := local.Line(); !strings.HasPrefix(got, "scope: local — no project bound") {
		t.Errorf("local line = %q", got)
	}
}
