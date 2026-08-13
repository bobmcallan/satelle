package config

// Active-workspace resolution for scoped sync (epic:scoped-sync, order:4). This
// is the orthogonal WHICH-workspace decision to the local|personal|shared TIER
// ladder in sync.go: the scope ladder decides a .satelle area's TIER, the active
// workspace decides the DESTINATION workspace a personal/shared area syncs
// through. Pure config — no network here; the sync engine (a later story)
// consumes ActiveWorkspace to map the handle to server-side paths.
//
// There are two distinct "workspace" concepts in satelle and they must not
// collide: internal/workspace (the multi-repo aggregate view, a global connected-
// repo registry) and THIS active-workspace binding (the scoped-sync destination,
// held under the repo [sync] workspace key). It lives there (not in
// internal/workspace) precisely to avoid the collision.

// PersonalWorkspace is the zero-config active workspace: every developer's own
// workspace. The default when [hosted] workspace is unset — nothing to configure
// for the common "sync to my own area" case. A login records nothing for it.
const PersonalWorkspace = "personal"

// Workspace kind values — the two kinds the hosted server returns (a sync
// "shared" SCOPE is a tier on the sync.go ladder, NOT a workspace kind; keep the
// two enums distinct).
const (
	// WorkspaceKindPersonal is the caller's own workspace (the default).
	WorkspaceKindPersonal = "personal"
	// WorkspaceKindTeam is a shared/team workspace multiple members sync to.
	WorkspaceKindTeam = "team"
)

// ActiveWorkspace is the workspace the sync engine routes personal+shared areas
// through. Name is the server workspace handle (the workspace name) the sync
// engine prefixes; Kind is personal (the default) or team.
type ActiveWorkspace struct {
	Name string
	Kind string
}

// IsPersonal reports whether the active workspace is the developer's own — the
// zero-config default, needing no binding.
func (a ActiveWorkspace) IsPersonal() bool { return a.Kind == WorkspaceKindPersonal }

// ResolveActiveWorkspace resolves the active workspace from [hosted] workspace.
// Unset, blank, or the personal sentinel resolves to the personal default —
// zero-config. Any other value is a team workspace the developer elected: an
// explicit value is never the personal default, because that needs no binding,
// so Kind is derivable (personal iff the value is the sentinel).
//
// The satelle.local.toml overlay is already merged by Load, so a per-developer
// choice recorded there overrides a committed team default transparently.
func (c Config) ResolveActiveWorkspace() ActiveWorkspace {
	w := c.SyncWorkspace()
	if w == "" || w == PersonalWorkspace {
		return ActiveWorkspace{Name: PersonalWorkspace, Kind: WorkspaceKindPersonal}
	}
	return ActiveWorkspace{Name: w, Kind: WorkspaceKindTeam}
}
