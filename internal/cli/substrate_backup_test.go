package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
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
	// AC6 + advisory: names the opt-in and local_only suppress (sty_84f14ace).
	if !strings.Contains(res.Notice, "hosted = true") || !strings.Contains(res.Notice, "local_only") {
		t.Errorf("expected advisory naming hosted opt-in + local_only, got %q", res.Notice)
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

// TestResolveBackupOptsHostedOptIn: [backup] hosted defaults off so a hosted-
// configured repo does not auto-push pre-images into the documents partition
// (sty_84f14ace AC5). Direct BackupOpts with server+project still pushes (tests
// and explicit call sites).
func TestResolveBackupOptsHostedOptIn(t *testing.T) {
	// Isolate global hosted config so ResolveHostedServer only sees repo config.
	t.Setenv("SATELLE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfg := config.Config{
		Hosted: config.HostedConfig{Server: "https://example.test", Project: "proj"},
	}
	opts := ResolveBackupOpts(cfg)
	if opts.HostedServer != "" || opts.HostedProject != "" {
		t.Fatalf("default Backup.Hosted=false must leave channel empty, got server=%q project=%q", opts.HostedServer, opts.HostedProject)
	}

	cfg.Backup.Hosted = true
	opts = ResolveBackupOpts(cfg)
	if opts.HostedServer != "https://example.test" || opts.HostedProject != "proj" {
		t.Fatalf("Backup.Hosted=true should resolve channel, got server=%q project=%q", opts.HostedServer, opts.HostedProject)
	}

	// AC5 / sty_0fd04503 AC4: inject HostedPush and assert it is never reached
	// when ResolveBackupOpts leaves HostedServer/Project empty (Backup.Hosted
	// false). Proves zero hosted pushes, not merely "no push-looking notice".
	dataDir := t.TempDir()
	opts = ResolveBackupOpts(config.Config{
		Hosted: config.HostedConfig{Server: "https://example.test", Project: "proj"},
		// Backup.Hosted false
	})
	pushes := 0
	opts.HostedPush = func(ctx context.Context, relPath string, body []byte) (string, error) {
		pushes++
		return "should-not-run", nil
	}
	res, err := backupExistingFile(dataDir, BackupKindPreMutation, "skills/x.md", []byte("pre"), opts)
	if err != nil {
		t.Fatal(err)
	}
	if pushes != 0 {
		t.Fatalf("default ResolveBackupOpts must not call HostedPush, got %d pushes", pushes)
	}
	if res.LocalPath == "" {
		t.Fatal("local floor must still be written (AC6)")
	}
	if _, err := os.Stat(res.LocalPath); err != nil {
		t.Fatalf("local backup missing: %v", err)
	}
	// No hosted notice (no channel).
	if strings.Contains(res.Notice, "hosted https") || strings.Contains(res.Notice, "hosted unavailable") {
		t.Errorf("default opts must not claim hosted push: %q", res.Notice)
	}
}

// TestBackupExistingFileLocalWriteFailureIsFatal (AC6): a local backup that
// cannot be written still errors — mandatory floor.
func TestBackupExistingFileLocalWriteFailureIsFatal(t *testing.T) {
	// Point dataDir at a file path so MkdirAll/write under it fails.
	fileAsDir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(fileAsDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := backupExistingFile(fileAsDir, BackupKindPreMutation, "skills/x.md", []byte("pre"), BackupOpts{})
	if err == nil {
		t.Fatal("local write failure must be fatal")
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

func TestPushBackupTreeHosted(t *testing.T) {
	root := t.TempDir()
	// two files under kinds
	for _, rel := range []string{"skills/a.md", "principles/b.md"} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(rel), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	n, msg := pushBackupTreeHosted(root, BackupOpts{
		HostedServer:  "https://example.test",
		HostedProject: "proj",
		HostedPush: func(_ context.Context, rel string, body []byte) (string, error) {
			got = append(got, rel)
			return "h://" + rel, nil
		},
	})
	if n != 2 {
		t.Fatalf("pushed %d, want 2; msg=%q got=%v", n, msg, got)
	}
	if !strings.Contains(msg, "pushed 2") {
		t.Errorf("msg = %q", msg)
	}
}

// TestHostedBackupWithholdsLocalFile (sty_698e70b6): a .local path is never
// hosted-pushed; the local backup still lands with a clear notice.
func TestHostedBackupWithholdsLocalFile(t *testing.T) {
	dataDir := t.TempDir()
	var pushes int
	res, err := backupExistingFile(dataDir, BackupKindRestore, "satelle.local.toml", []byte("SECRET=1\n"), BackupOpts{
		HostedServer:  "https://example.test",
		HostedProject: "proj",
		HostedPush: func(_ context.Context, rel string, body []byte) (string, error) {
			pushes++
			return "hosted://" + rel, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if pushes != 0 {
		t.Fatalf("HostedPush called %d times for .local path", pushes)
	}
	if !strings.Contains(res.Notice, "withheld") || !strings.Contains(res.Notice, ".local") {
		t.Errorf("notice should report withhold: %q", res.Notice)
	}
	if _, err := os.Stat(res.LocalPath); err != nil {
		t.Errorf("local backup must still exist: %v", err)
	}
}
