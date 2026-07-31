package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestAcquireLockRejectsSecondProcess is the regression test for duplicate
// bot instances: a real second process must fail to take the lock while
// this one holds it. The old read-PID-then-write scheme passed a
// same-process test but let two concurrent starters both win.
func TestAcquireLockRejectsSecondProcess(t *testing.T) {
	if os.Getenv("PIGEON_LOCK_CHILD") != "" {
		// Child half: try to take the lock the parent already holds.
		if err := acquireLock(os.Getenv("PIGEON_LOCK_PATH")); err != nil {
			os.Exit(3) // expected: lock is held
		}
		os.Exit(0) // acquired — duplicate instance would have started
	}

	path := filepath.Join(t.TempDir(), "pigeon-claw.pid")
	if err := acquireLock(path); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	defer releaseLock()

	cmd := exec.Command(os.Args[0], "-test.run=TestAcquireLockRejectsSecondProcess")
	cmd.Env = append(os.Environ(), "PIGEON_LOCK_CHILD=1", "PIGEON_LOCK_PATH="+path)
	err := cmd.Run()

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("second process should have been rejected, got err=%v", err)
	}
	if code := exitErr.ExitCode(); code != 3 {
		t.Fatalf("second process exit code = %d, want 3 (lock held)", code)
	}
}

// TestAcquireLockIgnoresStaleFileContent pins the duplicate-instance bug.
// The old scheme trusted the *contents* of the PID file: read a PID, and if
// that process looked dead, take over. Any way the recorded PID went stale
// while a bot was still running — the file rewritten, deleted and recreated
// by !restart / auto-update, or two starters both reading a dead PID left by
// the previous run — let a second instance start, which is how two bots ended
// up on one Discord token and every command ran twice.
//
// flock moves the authority from the file's contents to the descriptor, so a
// stale PID on disk can no longer hand out a second lock.
func TestAcquireLockIgnoresStaleFileContent(t *testing.T) {
	if os.Getenv("PIGEON_LOCK_CHILD") != "" {
		if err := acquireLock(os.Getenv("PIGEON_LOCK_PATH")); err != nil {
			os.Exit(3) // expected: the holder's flock still stands
		}
		os.Exit(0) // acquired despite a live holder — duplicate instance
	}

	path := filepath.Join(t.TempDir(), "pigeon-claw.pid")
	if err := acquireLock(path); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	defer releaseLock()

	// Stale the recorded PID out from under the live holder: above the
	// macOS PID ceiling, so it can never name a running process.
	if err := os.WriteFile(path, []byte("4194303"), 0644); err != nil {
		t.Fatalf("write stale pid: %v", err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestAcquireLockIgnoresStaleFileContent")
	cmd.Env = append(os.Environ(), "PIGEON_LOCK_CHILD=1", "PIGEON_LOCK_PATH="+path)
	err := cmd.Run()

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("a stale PID must not grant a second lock while a holder is live, got err=%v", err)
	}
	if code := exitErr.ExitCode(); code != 3 {
		t.Fatalf("second process exit code = %d, want 3 (lock held)", code)
	}
}

// TestAcquireLockAfterReleaseSucceeds covers the restart path: once the
// holder releases (or execs, which closes the O_CLOEXEC descriptor), the
// replacement must be able to take the lock.
func TestAcquireLockAfterReleaseSucceeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pigeon-claw.pid")
	if err := acquireLock(path); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	releaseLock()

	if err := acquireLock(path); err != nil {
		t.Fatalf("re-acquire after release failed: %v", err)
	}
	releaseLock()
}

// TestAcquireLockRecordsPID keeps `pigeon-claw status` and the duplicate
// error message useful.
func TestAcquireLockRecordsPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pigeon-claw.pid")
	if err := acquireLock(path); err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	defer releaseLock()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid != os.Getpid() {
		t.Fatalf("lock file = %q, want PID %d", data, os.Getpid())
	}
}

// TestReleaseLockKeepsFile guards the flock invariant: deleting the file
// would create a second lockable inode and reopen the duplicate-instance
// hole that !restart and the auto-updater used to have.
func TestReleaseLockKeepsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pigeon-claw.pid")
	if err := acquireLock(path); err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	releaseLock()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lock file should survive release: %v", err)
	}
}
