package web

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/mirror"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// mirrorIdentity loads kind=identity id=meta for a partition.
func mirrorIdentity(ctx context.Context, s *mirror.Store, repoKey string) mirror.IdentityMeta {
	payload, err := s.GetItem(ctx, repoKey, "identity", "meta")
	if err != nil || payload == "" {
		return mirror.IdentityMeta{}
	}
	var id mirror.IdentityMeta
	_ = json.Unmarshal([]byte(payload), &id)
	return id
}

// partitionFreshness reports when this partition was last confirmed against its
// repo and whether that is old enough that the view must not present itself as
// current (sty_e6e467fe). An unknown partition is stale with a zero time: the
// page then says it has no confirmation at all, which is the honest reading.
func partitionFreshness(ctx context.Context, s *mirror.Store, repoKey string) (time.Time, bool) {
	p, ok, err := s.GetPartition(ctx, repoKey)
	if err != nil || !ok {
		return time.Time{}, true
	}
	last, _ := p.LastIngest()
	return last.Local(), p.Stale(time.Now())
}

// mirrorLoadPanels assembles pageData exclusively from mirror kinds (no repo DB).
func mirrorLoadPanels(ctx context.Context, s *mirror.Store, repoKey, slug string) (pageData, mirror.IdentityMeta, error) {
	id := mirrorIdentity(ctx, s, repoKey)

	stories, err := decodeItems(ctx, s, repoKey, "story")
	if err != nil {
		return pageData{}, id, err
	}
	tasks, err := decodeItems(ctx, s, repoKey, "task")
	if err != nil {
		return pageData{}, id, err
	}

	docs, err := decodeDocs(ctx, s, repoKey)
	if err != nil {
		return pageData{}, id, err
	}
	byKind := map[string][]docindex.Doc{}
	prov, src := map[string]string{}, map[string]string{}
	for _, d := range docs {
		byKind[d.Kind] = append(byKind[d.Kind], d.Doc)
		key := d.Kind + "\x00" + d.Name
		if d.Provenance != "" {
			prov[key] = d.Provenance
		}
		if d.Source != "" {
			src[key] = d.Source
		}
	}
	kinds := make([]kindGroup, 0, len(config.AuthoredKinds))
	for _, k := range config.AuthoredKinds {
		dd := byKind[k]
		rows := make([]docRowVM, 0, len(dd))
		for _, d := range dd {
			key := d.Kind + "\x00" + d.Name
			rows = append(rows, docRowVM{
				Name: d.Name, Headline: d.Headline, ModTime: d.ModTime,
				Provenance: prov[key], Source: src[key],
			})
		}
		kinds = append(kinds, kindGroup{Kind: k, Docs: rows})
	}

	backlog := 0
	for _, st := range stories {
		if st.Status == workitem.StatusBacklog {
			backlog++
		}
	}

	entriesByStory, _ := decodeLedgerByStory(ctx, s, repoKey)
	liveSeat, _ := decodeLiveSeats(ctx, s, repoKey)
	engagedIDs, _ := decodeEngagedStorySeats(ctx, s, repoKey)
	catStepOf := categoryStepOf(byKind["workflows"])

	projectName := id.ProjectName
	if projectName == "" {
		projectName = slug
	}
	repoRoot := id.RepoRoot
	if repoRoot == "" {
		repoRoot = slug
	}

	lastIngest, stale := partitionFreshness(ctx, s, repoKey)

	return pageData{
		RepoRoot:        repoRoot,
		ProjectName:     projectName,
		LastIngest:      lastIngest,
		Stale:           stale,
		Stories:         attachLightsFrom(entriesByStory, stories, liveSeat, catStepOf),
		BacklogCount:    backlog,
		EngagementCount: len(engagedIDs),
		EngagedStoryIDs: engagedIDs,
		Tasks:           attachLightsFrom(entriesByStory, tasks, liveSeat, catStepOf),
		DocKinds:        kinds,
		DocCount:        len(docs),
		Workflows:       workflowRows(byKind["workflows"], prov, src),
		Uptime:          formatUptime(time.Since(serverStart)),
		Theme:           "", // client localStorage only on the mirror
		TopBar:          mirrorTopBar("", id.FooterEmail),
	}, id, nil
}

// attachLightsFrom is the mirror-side attachLights: ledger rows already loaded.
func attachLightsFrom(entriesByStory map[string][]ledger.Entry, items []workitem.Item, liveSeat map[string]bool, catStepOf func(category, state string) int) []rowVM {
	out := make([]rowVM, len(items))
	for i, it := range items {
		stepOf := func(s string) int { return catStepOf(it.Category, s) }
		out[i] = rowVM{Item: it, Lights: buildLights(entriesByStory[it.ID], it.Status, liveSeat[it.ID], stepOf)}
	}
	return out
}

func decodeItems(ctx context.Context, s *mirror.Store, repoKey, kind string) ([]workitem.Item, error) {
	rows, err := s.ListItems(ctx, repoKey, kind)
	if err != nil {
		return nil, err
	}
	out := make([]workitem.Item, 0, len(rows))
	for _, r := range rows {
		var it workitem.Item
		if err := json.Unmarshal([]byte(r.Payload), &it); err != nil {
			continue
		}
		if it.ID == "" {
			it.ID = r.ID
		}
		out = append(out, it)
	}
	return out, nil
}

// mirrorDoc is a doc payload with optional provenance/source from the snapshot.
type mirrorDoc struct {
	docindex.Doc
	Provenance string `json:"provenance"`
	Source     string `json:"source"`
}

func decodeDocs(ctx context.Context, s *mirror.Store, repoKey string) ([]mirrorDoc, error) {
	rows, err := s.ListItems(ctx, repoKey, "doc")
	if err != nil {
		return nil, err
	}
	out := make([]mirrorDoc, 0, len(rows))
	for _, r := range rows {
		var d mirrorDoc
		if err := json.Unmarshal([]byte(r.Payload), &d); err != nil {
			continue
		}
		if d.Name == "" {
			d.Name = r.ID
		}
		out = append(out, d)
	}
	return out, nil
}

func decodeLedgerByStory(ctx context.Context, s *mirror.Store, repoKey string) (map[string][]ledger.Entry, error) {
	rows, err := s.ListItems(ctx, repoKey, "ledger_event")
	if err != nil {
		return nil, err
	}
	out := map[string][]ledger.Entry{}
	for _, r := range rows {
		var e ledger.Entry
		if err := json.Unmarshal([]byte(r.Payload), &e); err != nil {
			continue
		}
		if e.ID == "" {
			e.ID = r.ID
		}
		if e.StoryID == "" {
			continue
		}
		out[e.StoryID] = append(out[e.StoryID], e)
	}
	// ListAll is newest-first under the export budget (sty_c5065d05);
	// ReplaceKind order is not guaranteed — sort each story's strip by CreatedAt.
	for sid, es := range out {
		sortLedger(es)
		out[sid] = es
	}
	return out, nil
}

func sortLedger(es []ledger.Entry) {
	sort.SliceStable(es, func(i, j int) bool {
		if es[i].CreatedAt.Equal(es[j].CreatedAt) {
			return es[i].ID < es[j].ID
		}
		return es[i].CreatedAt.Before(es[j].CreatedAt)
	})
}

func decodeLiveSeats(ctx context.Context, s *mirror.Store, repoKey string) (map[string]bool, error) {
	rows, err := s.ListItems(ctx, repoKey, "seat")
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, r := range rows {
		var seat struct {
			ID       string `json:"id"`
			InFlight bool   `json:"in_flight"`
			Stale    bool   `json:"stale"`
		}
		if err := json.Unmarshal([]byte(r.Payload), &seat); err != nil {
			continue
		}
		if seat.ID == "" {
			seat.ID = r.ID
		}
		if seat.InFlight && !seat.Stale {
			out[seat.ID] = true
		}
	}
	return out, nil
}

// seatPayload is the mirror seat row shape pushed by listSeatsJSON.
type seatPayload struct {
	ID        string `json:"id"`
	StorySeat bool   `json:"story_seat"`
	InFlight  bool   `json:"in_flight"`
	Stale     bool   `json:"stale"`
}

// engagedStorySeatIDs returns non-stale story seats (story_seat && !stale).
// Distinct from decodeLiveSeats (in_flight && !stale) used only for progress
// lights — a settled engaged lease still counts as engaged (sty_01ba9482).
func engagedStorySeatIDs(seats []seatPayload) []string {
	var ids []string
	for _, seat := range seats {
		if !seat.StorySeat || seat.Stale {
			continue
		}
		id := strings.TrimSpace(seat.ID)
		if id == "" {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// decodeEngagedStorySeats lists non-stale story-seat holders for engagement chrome.
func decodeEngagedStorySeats(ctx context.Context, s *mirror.Store, repoKey string) ([]string, error) {
	rows, err := s.ListItems(ctx, repoKey, "seat")
	if err != nil {
		return nil, err
	}
	seats := make([]seatPayload, 0, len(rows))
	for _, r := range rows {
		var seat seatPayload
		if err := json.Unmarshal([]byte(r.Payload), &seat); err != nil {
			continue
		}
		if seat.ID == "" {
			seat.ID = r.ID
		}
		seats = append(seats, seat)
	}
	return engagedStorySeatIDs(seats), nil
}

// mirrorLoadDetail builds the expand/detail VM from mirror kinds only.
func mirrorLoadDetail(ctx context.Context, s *mirror.Store, repoKey, group, id string) (detailData, mirror.IdentityMeta, error) {
	kind := "story"
	if group == "task" {
		kind = "task"
	}
	payload, err := s.GetItem(ctx, repoKey, kind, id)
	if err != nil {
		return detailData{}, mirror.IdentityMeta{}, err
	}
	var item workitem.Item
	if err := json.Unmarshal([]byte(payload), &item); err != nil {
		return detailData{}, mirror.IdentityMeta{}, err
	}
	if item.ID == "" {
		item.ID = id
	}

	entriesByStory, _ := decodeLedgerByStory(ctx, s, repoKey)
	events := entriesByStory[id]
	// Reverse for timeline (newest first) — same as loadDetail.
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	evs := make([]eventVM, len(events))
	for i, e := range events {
		evs[i] = eventVM{Entry: e, Chips: eventChips(e)}
	}

	// Story docs from mirror story_doc kind (id = story_id/name). The route
	// document is lifted out of the list into its own section — it is the story's
	// route and the reasoning behind every outcome, not one attachment among many.
	var docs []storyDocVM
	var route *storyDocVM
	if sdocs, err := s.ListItems(ctx, repoKey, "story_doc"); err == nil {
		prefix := id + "/"
		for _, r := range sdocs {
			if !strings.HasPrefix(r.ID, prefix) && !strings.HasPrefix(r.ID, id+"_") {
				// accept id = "storyID/name" form
				if !strings.HasPrefix(r.ID, id) {
					continue
				}
			}
			var ref struct {
				Name string `json:"name"`
				Type string `json:"type"`
				Body string `json:"body"`
			}
			_ = json.Unmarshal([]byte(r.Payload), &ref)
			name := ref.Name
			if name == "" {
				name = filepath.Base(r.ID)
			}
			if group == "task" && (strings.HasPrefix(name, "exe_") || strings.HasPrefix(name, "output-")) {
				continue
			}
			vm := storyDocVM{Name: name, Type: ref.Type, HTML: renderMarkdown(ref.Body)}
			if name == routeDocName {
				route = &vm
				continue
			}
			docs = append(docs, vm)
		}
	}

	var executions []executionVM
	if group == "task" {
		if runs, err := decodeItems(ctx, s, repoKey, "execution"); err == nil {
			for _, run := range runs {
				if run.ParentID != id {
					continue
				}
				executions = append(executions, executionVM{
					ID: run.ID, Status: run.Status, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
				})
			}
		}
	}

	idMeta := mirrorIdentity(ctx, s, repoKey)
	return detailData{
		Item: item, Events: evs, Route: route, Docs: docs, Executions: executions,
		TopBar: mirrorTopBar("", idMeta.FooterEmail),
	}, idMeta, nil
}

// routeDocName is the name verb.RouteDocName writes the route document under.
// Duplicated rather than imported: internal/serve must link neither verb nor the
// rest of the repo-writing stack (internal/serve/deps_test.go).
const routeDocName = "route"

// mirrorTopBar builds chrome for the push-fed surface: no auth forms; optional
// identity email from the pushed meta blob (order:4).
func mirrorTopBar(active, identityEmail string) topBar {
	tb := newTopBar(active)
	tb.User = nil
	tb.IdentityEmail = identityEmail
	tb.MirrorRO = true
	return tb
}

// mirrorSettingsData loads the pushed settings blob into the settings template VM.
func mirrorSettingsData(ctx context.Context, s *mirror.Store, repoKey string) (settingsData, mirror.IdentityMeta, error) {
	id := mirrorIdentity(ctx, s, repoKey)
	payload, err := s.GetItem(ctx, repoKey, "settings", "config")
	if err != nil || payload == "" {
		return settingsData{
			TopBar:   mirrorTopBar("", id.FooterEmail),
			RepoRoot: id.RepoRoot,
			Rows:     nil,
			MirrorRO: true,
		}, id, nil
	}
	var blob struct {
		RepoRoot string `json:"repo_root"`
		Rows     []struct {
			FieldID string `json:"field_id"`
			Label   string `json:"label"`
			Help    string `json:"help"`
			Value   string `json:"value"`
			Section string `json:"section"`
		} `json:"rows"`
	}
	_ = json.Unmarshal([]byte(payload), &blob)
	lastSect := "\x00"
	var rows []settingsRowVM
	for _, r := range blob.Rows {
		vm := settingsRowVM{FieldID: r.FieldID, Label: r.Label, Help: r.Help, Value: r.Value}
		sect := r.Section
		if sect != lastSect {
			vm.SectHead = sectionLabel(sect)
			lastSect = sect
		}
		rows = append(rows, vm)
	}
	root := blob.RepoRoot
	if root == "" {
		root = id.RepoRoot
	}
	return settingsData{
		Rows:     rows,
		TopBar:   mirrorTopBar("", id.FooterEmail),
		RepoRoot: root,
		MirrorRO: true,
	}, id, nil
}

// findDoc returns one mirrored doc by kind+name.
func findDoc(ctx context.Context, s *mirror.Store, repoKey, kind, name string) (mirrorDoc, error) {
	docs, err := decodeDocs(ctx, s, repoKey)
	if err != nil {
		return mirrorDoc{}, err
	}
	for _, d := range docs {
		if d.Kind == kind && d.Name == name {
			return d, nil
		}
	}
	return mirrorDoc{}, errNotFound
}

// errNotFound is a sentinel for missing mirror rows.
var errNotFound = errString("not found")

type errString string

func (e errString) Error() string { return string(e) }
