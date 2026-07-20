//go:build integration

package tests

import (
	"database/sql"
	"os"

	_ "modernc.org/sqlite"
)

func init() {
	queryMirrorPartitionKeys = queryMirrorPartitionKeysImpl
}

// queryMirrorPartitionKeysImpl lists partitions.repo_key from a mirror.db path
// (sty_5aa08259 AC1 host-plane guard). Read-only open; ignore missing tables.
func queryMirrorPartitionKeysImpl(dbPath string) map[string]struct{} {
	out := map[string]struct{}{}
	if dbPath == "" {
		return out
	}
	if _, err := os.Stat(dbPath); err != nil {
		return out
	}
	// mode=ro so we never write the operator WAL from the suite process.
	db, err := sql.Open("sqlite", dbPath+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return out
	}
	defer db.Close()
	rows, err := db.Query(`SELECT repo_key FROM partitions`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			continue
		}
		if k != "" {
			out[k] = struct{}{}
		}
	}
	return out
}
