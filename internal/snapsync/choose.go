package snapsync

import "fmt"

// Deliberate reconcile (sty_b0873ec3): the operator picks a side for a path that
// differs between this machine and the hosted copy, instead of taking the
// three-way default. Everything that decides what a choice does lives here, pure
// over the plan; the driver carries it out.

// Side is the operator's choice for one path that differs between this machine
// and the hosted copy.
type Side int

const (
	// TakeLocal keeps this machine's copy; the next push publishes it.
	TakeLocal Side = iota + 1
	// TakeHosted replaces this machine's copy with the hosted one, or removes it
	// where the hosted copy has none.
	TakeHosted
)

// Origin names whose bytes a file holds.
type Origin int

const (
	OriginNone Origin = iota
	OriginLocal
	OriginHosted
)

func (o Origin) String() string {
	switch o {
	case OriginLocal:
		return "local"
	case OriginHosted:
		return "hosted"
	}
	return ""
}

// Describe names how a path differs and what a plain pull does with it.
func Describe(e Entry) (class, def string) {
	switch e.Action {
	case Write:
		if e.Local == "" {
			return "added on hosted only", "take hosted"
		}
		return "changed on hosted only", "take hosted"
	case Delete:
		return "deleted on hosted only", "take hosted (remove here)"
	case KeepLocal:
		if e.Remote == "" {
			return "added here only", "keep local"
		}
		return "changed here only", "keep local"
	case LocalDeleted:
		return "deleted here only", "keep local (the next push drops it)"
	case Conflict:
		return "changed on both", "park the hosted copy as a conflict"
	case Incomplete:
		return "hosted bytes incomplete", "leave unchanged"
	case RemovedStale:
		return "stale copy of a hosted deletion", "move aside"
	case LocalOnly:
		return "added here only (never published)", "keep local"
	case DeletedRemotelyKept:
		return "deleted on hosted, changed here", "keep local"
	case StillUnmerged:
		return "still unmerged", "stay unmerged"
	}
	return "in step", "-"
}

// Choose applies the operator's side for one path to the plan: it rewrites the
// path's entry and the base this machine will hold, and returns whose bytes the
// choice replaces. It reads and writes nothing else. remote is the effective
// snapshot and heads the hosted heads, which say whether hosted bytes a choice
// needs are all there. A path that does not differ, is not in the plan, or whose
// needed hosted bytes are incomplete is refused and left as planned.
func (p *PullPlan) Choose(remote Record, heads map[string]string, path string, side Side) (Origin, error) {
	if side != TakeLocal && side != TakeHosted {
		return OriginNone, fmt.Errorf("%s: unknown choice", path)
	}
	var e *Entry
	for i := range p.Entries {
		if p.Entries[i].Path == path {
			e = &p.Entries[i]
			break
		}
	}
	if e == nil {
		return OriginNone, fmt.Errorf("%s: not in this area", path)
	}
	incomplete := fmt.Errorf("%s: hosted bytes incomplete — retry after the upload lands", path)
	needHosted := func(sha string) error {
		if sha == "" || heads[path] != sha {
			return incomplete
		}
		return nil
	}
	saveLocal := func() Origin {
		if e.Local == "" {
			return OriginNone
		}
		return OriginLocal
	}
	// keep leaves the local file as it is: absent stays a deletion the next push
	// publishes.
	keep := func() {
		if e.Local == "" {
			e.Action = LocalDeleted
		} else {
			e.Action = KeepLocal
		}
	}
	unmerge := func() {
		delete(p.Base.Unmerged, path)
		if len(p.Base.Unmerged) == 0 {
			p.Base.Unmerged = nil
		}
	}

	var saved Origin
	switch e.Action {
	case InSync:
		return OriginNone, fmt.Errorf("%s: does not differ", path)
	case Incomplete:
		return OriginNone, incomplete
	case Write:
		if side == TakeLocal {
			keep()
			saved = OriginHosted
		} else {
			saved = saveLocal()
		}
	case Delete:
		if side == TakeLocal {
			e.Action = KeepLocal // absent from the base, so the next push adds it
		} else {
			saved = saveLocal()
		}
	case KeepLocal:
		if side == TakeHosted {
			saved = saveLocal()
			if e.Remote == "" {
				e.Action = Delete
				break
			}
			if err := needHosted(e.Remote); err != nil {
				return OriginNone, err
			}
			e.Action = Write
			p.Base.Files[path] = e.Remote
		}
	case LocalDeleted:
		if side == TakeHosted {
			if err := needHosted(e.Remote); err != nil {
				return OriginNone, err
			}
			e.Action = Write
		}
	case Conflict:
		unmerge()
		p.Base.Files[path] = e.Remote
		if side == TakeLocal {
			e.Action = KeepLocal
			saved = OriginHosted
		} else {
			e.Action = Write
			saved = saveLocal()
		}
	case RemovedStale:
		if side == TakeLocal {
			e.Action = LocalOnly
		} else {
			saved = saveLocal()
		}
	case LocalOnly, DeletedRemotelyKept:
		if side == TakeHosted {
			e.Action = Delete
			saved = saveLocal()
		}
	case StillUnmerged:
		cur, inRemote := remote.Files[path]
		if side == TakeHosted && inRemote {
			if err := needHosted(cur); err != nil {
				return OriginNone, err
			}
		}
		unmerge()
		if inRemote {
			p.Base.Files[path] = cur
		} else {
			delete(p.Base.Files, path)
		}
		e.ClearCopy, e.Remote = true, cur
		switch {
		case side == TakeLocal:
			keep()
			saved = OriginHosted // the parked copy
		case inRemote:
			e.Action = Write
			saved = saveLocal()
		default:
			e.Action = Delete
			saved = saveLocal()
		}
	}
	e.Chosen, e.Overridden = side, saved
	return saved, nil
}

// ChooseAll applies the side to every path that differs and can be chosen
// (everything but in step and incomplete) and that has no choice yet.
func ChooseAll(plan PullPlan, choices map[string]Side, side Side) map[string]Side {
	if choices == nil {
		choices = map[string]Side{}
	}
	for _, e := range plan.Entries {
		if e.Action == InSync || e.Action == Incomplete {
			continue
		}
		if _, ok := choices[e.Path]; !ok {
			choices[e.Path] = side
		}
	}
	return choices
}
