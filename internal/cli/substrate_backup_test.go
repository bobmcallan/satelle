package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupExistingFileLocalFloorAndAdvisory(t *testing.T) {
	dataDir := t.TempDir()
	body := []byte("pre-image\n")
	res, err := backupExistingFile(dataDir, BackupKindPreMutation, "skills/x.md", body, BackupOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filepath.ToSlash(res.LocalPath), "backups/pre-mutation/skills/x.md") {
		t.Fatalf("local path = %s", res.LocalPath)
	}
	got, err := os.ReadFile(res.LocalPath)
	if err != nil || string(got) != string(body) {
		t.Fatalf("backup content wrong: %v %q", err, got)
	}
	if !strings.Contains(res.Notice, "online/personal backup") {
		t.Errorf("expected advisory notice, got %q", res.Notice)
	}

	// local_only suppresses advisory
	res2, err := backupExistingFile(dataDir, BackupKindPreMutation, "skills/y.md", body, BackupOpts{LocalOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Notice != "" {
		t.Errorf("local_only should suppress advisory, got %q", res2.Notice)
	}
}

func TestBackupExistingFileHostedPush(t *testing.T) {
	dataDir := t.TempDir()
	var pushed string
	res, err := backupExistingFile(dataDir, BackupKindRestore, "principles/p.md", []byte("x"), BackupOpts{
		HostedServer:  "https://example.test",
		HostedProject: "proj",
		HostedPush: func(_ context.Context, rel string, body []byte) (string, error) {
			pushed = rel
			return "hosted://" + rel, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if pushed != "principles/p.md" {
		t.Errorf("hosted push rel = %q", pushed)
	}
	if !strings.Contains(res.Notice, "hosted") || !strings.Contains(res.Notice, "local") {
		t.Errorf("notice should name local+hosted: %q", res.Notice)
	}
}

func TestBackupExistingFileHostedDegrades(t *testing.T) {
	dataDir := t.TempDir()
	res, err := backupExistingFile(dataDir, BackupKindRestore, "principles/p.md", []byte("x"), BackupOpts{
		HostedServer:  "https://example.test",
		HostedProject: "proj",
		HostedPush: func(_ context.Context, rel string, body []byte) (string, error) {
			return "", context.DeadlineExceeded
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Notice, "hosted unavailable") {
		t.Errorf("want degrade notice, got %q", res.Notice)
	}
	if _, err := os.Stat(res.LocalPath); err != nil {
		t.Errorf("local backup must still exist: %v", err)
	}
}

func TestBackupExistingPathAbsentIsNoop(t *testing.T) {
	dataDir := t.TempDir()
	res, err := backupExistingPath(dataDir, BackupKindRestore, "missing.md", filepath.Join(dataDir, "missing.md"), BackupOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if res.LocalPath != "" {
		t.Errorf("absent file should not create backup, got %s", res.LocalPath)
	}
}
