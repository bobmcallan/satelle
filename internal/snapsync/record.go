// Package snapsync makes substrate sync behave like pushing to and pulling from a
// git remote (sty_fe5a8ed4), without git's machinery.
//
// A push publishes the WHOLE state of a sync area as one snapshot record — a
// small JSON document naming every file in the area and its sha, plus the
// snapshot it was based on (its parent). A file missing from the record is a
// file deleted, which is how a delete or rename reaches another machine: the
// hosted store only ever appends per-path heads and cannot delete one. A push
// is fast-forward only (refused when this machine's view is older than the
// hosted snapshot), and a pull is a three-way comparison per path over what the
// machine last synced (its base), what is on disk, and the hosted snapshot.
//
// The hosted server is unchanged: records are ordinary config-route files under
// backups/, which no binary ever materialises into a tree.
package snapsync

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	recordPrefix = "backups/sync/"
	recordSuffix = ".snapshot.json"
)

// RecordPath is the config-route path of an area's snapshot record. It is
// always stored on the config route — even for the documents area — because
// only that route can read a pinned version, which is how history is walked.
// It sits under backups/ so every binary's Restore skips it: no binary, old or
// new, ever writes it into a tree.
func RecordPath(area string) string { return recordPrefix + area + recordSuffix }

// AreaOfRecordPath reports whether p is a snapshot record path and, if so, for
// which area.
func AreaOfRecordPath(p string) (string, bool) {
	if !strings.HasPrefix(p, recordPrefix) || !strings.HasSuffix(p, recordSuffix) {
		return "", false
	}
	area := strings.TrimSuffix(strings.TrimPrefix(p, recordPrefix), recordSuffix)
	if area == "" || strings.Contains(area, "/") {
		return "", false
	}
	return area, true
}

// Record is one snapshot of a sync area.
type Record struct {
	// Parent is the effective snapshot version this one was based on; 0 for the
	// first. Two records naming the same parent are a race, and the lowest
	// version wins (see Effective).
	Parent int `json:"parent"`
	// By is the pushing checkout's location id (informational).
	By string `json:"by,omitempty"`
	// Nonce makes every claim a new head on the server, so a claim is never
	// swallowed as an idempotent re-push of identical content.
	Nonce string `json:"nonce"`
	// Files maps each file in the area to its sha256 (hex). Absent = deleted.
	Files map[string]string `json:"files"`
}

// NewRecord builds a claim on top of parent for the given files.
func NewRecord(parent int, by string, files map[string]string) (Record, error) {
	var n [8]byte
	if _, err := rand.Read(n[:]); err != nil {
		return Record{}, fmt.Errorf("snapsync: nonce: %w", err)
	}
	cp := make(map[string]string, len(files))
	for p, s := range files {
		cp[p] = s
	}
	return Record{Parent: parent, By: by, Nonce: hex.EncodeToString(n[:]), Files: cp}, nil
}

// Encode renders the record deterministically (encoding/json sorts map keys).
func (r Record) Encode() []byte {
	b, _ := json.MarshalIndent(r, "", "  ")
	return append(b, '\n')
}

// Decode parses a record. A body that is not a record (or has no file map) is an
// error, so a corrupt version is skipped by Effective rather than trusted.
func Decode(b []byte) (Record, error) {
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		return Record{}, fmt.Errorf("snapsync: decode record: %w", err)
	}
	if r.Files == nil {
		return Record{}, fmt.Errorf("snapsync: record has no files map")
	}
	return r, nil
}

// ReadFunc reads the record at a pinned version. ok=false means there is no
// valid record at that version (not found, or not decodable) — Effective skips
// it. A non-nil error is a transport failure and aborts the walk.
type ReadFunc func(version int) (rec Record, ok bool, err error)

// Effective returns the effective snapshot at or below head, walking forward
// from base (a version already known to be effective; 0 when there is none).
//
// A record counts only when its parent is the effective version at that point,
// and the LOWEST version naming a given parent wins. That is what makes a
// refused push harmless: the hosted store appends every PUT as a head and never
// deletes one, so a machine that lost a race leaves a record behind — but its
// parent is already superseded, so every reader ignores it.
//
// version 0 with a zero Record means the area has no snapshot.
func Effective(base, head int, read ReadFunc) (int, Record, error) {
	if base > head {
		base = 0 // a base ahead of the hosted head belongs to a replaced store
	}
	eff := base
	var rec Record
	have := false
	for v := base + 1; v <= head; v++ {
		r, ok, err := read(v)
		if err != nil {
			return 0, Record{}, err
		}
		if !ok || r.Parent != eff {
			continue
		}
		eff, rec, have = v, r, true
	}
	if !have && eff > 0 {
		r, ok, err := read(eff)
		if err != nil {
			return 0, Record{}, err
		}
		if !ok {
			return 0, Record{}, fmt.Errorf("snapsync: snapshot %d is not readable on the hosted copy", eff)
		}
		rec = r
	}
	return eff, rec, nil
}
