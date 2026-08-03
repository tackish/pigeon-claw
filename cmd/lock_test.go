package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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

// TestAcquireLockRefusesOldSchemeHolder covers the upgrade window that
// actually bit: a binary from before the file lock records its PID and
// never flocks, so the lock is free and a current binary happily starts
// alongside it — two bots on one Discord token, with every command run
// twice and every message answered twice.
//
// Simulated by writing a live pigeon-claw-ish PID with no flock held,
// which is exactly the on-disk state such a binary leaves.
func TestAcquireLockRefusesOldSchemeHolder(t *testing.T) {
	if os.Getenv("PIGEON_LOCK_OLD_HOLDER") != "" {
		// Stand in for the old binary: stay alive with our PID recorded
		// in the lock file and no flock held.
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "pigeon-claw.pid")

	// The guard matches on the holder's command, so the stand-in has to
	// actually be named pigeon-claw. Copy this test binary rather than a
	// system one — a copied system binary fails macOS signature checks
	// and is killed before it can hold anything.
	standIn := filepath.Join(dir, "pigeon-claw")
	self, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Skipf("cannot read test binary: %v", err)
	}
	if err := os.WriteFile(standIn, self, 0755); err != nil {
		t.Fatalf("write stand-in: %v", err)
	}

	holder := exec.Command(standIn, "-test.run=TestAcquireLockRefusesOldSchemeHolder")
	holder.Env = append(os.Environ(), "PIGEON_LOCK_OLD_HOLDER=1")
	if err := holder.Start(); err != nil {
		t.Fatalf("start stand-in: %v", err)
	}
	defer func() {
		_ = holder.Process.Kill()
		_, _ = holder.Process.Wait()
	}()

	// Wait for it to be visible to ps under the pigeon-claw name.
	deadline := time.Now().Add(5 * time.Second)
	for {
		out, _ := exec.Command("ps", "-p", strconv.Itoa(holder.Process.Pid), "-o", "command=").Output()
		if strings.Contains(string(out), "pigeon-claw") {
			break
		}
		if time.Now().After(deadline) {
			t.Skipf("stand-in never appeared as pigeon-claw in ps: %q", out)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Exactly the on-disk state an old binary leaves: its PID recorded,
	// no flock held.
	if err := os.WriteFile(path, []byte(strconv.Itoa(holder.Process.Pid)), 0644); err != nil {
		t.Fatalf("write old-scheme pid file: %v", err)
	}

	err = acquireLock(path)
	if err == nil {
		releaseLock()
		t.Fatalf("started alongside a live old-scheme instance (PID %d)", holder.Process.Pid)
	}
	if !strings.Contains(err.Error(), strconv.Itoa(holder.Process.Pid)) {
		t.Fatalf("error should name the holder PID, got: %v", err)
	}
}

// TestAcquireLockIgnoresRecycledPID: refusing to start because an
// unrelated program inherited a dead instance's PID would be worse than
// the duplicate the check guards against.
func TestAcquireLockIgnoresRecycledPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pigeon-claw.pid")

	// A long-lived process that is definitely not pigeon-claw.
	other := exec.Command("sleep", "30")
	if err := other.Start(); err != nil {
		t.Fatalf("start unrelated process: %v", err)
	}
	defer func() {
		_ = other.Process.Kill()
		_, _ = other.Process.Wait()
	}()

	if err := os.WriteFile(path, []byte(strconv.Itoa(other.Process.Pid)), 0644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	if err := acquireLock(path); err != nil {
		t.Fatalf("a recycled PID must not block startup: %v", err)
	}
	releaseLock()
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
