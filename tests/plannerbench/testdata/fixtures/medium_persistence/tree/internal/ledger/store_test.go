package ledger

import (
	"strings"
	"testing"
	"time"
)

func TestAppendRejectsSecretShapedID(t *testing.T) {
	s, err := Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(Row{ID: "api_key=abc", Step: "plan"}); err == nil {
		t.Fatal("secret-shaped id accepted")
	}
	if err := s.Append(Row{ID: "row-1", Step: "plan", DurationMS: 12, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Rows()
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
}

func TestMigrationsAreAppendOnly(t *testing.T) {
	if len(Migrations) < 3 || !strings.HasPrefix(Migrations[0], "CREATE TABLE") {
		t.Fatalf("migration list changed shape: %v", Migrations)
	}
	for i, stmt := range Migrations[1:] {
		if !strings.HasPrefix(stmt, "ALTER TABLE") {
			t.Fatalf("migration %d must be an additive ALTER: %q", i+2, stmt)
		}
	}
}
