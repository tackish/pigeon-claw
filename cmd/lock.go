package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// lockFile holds this process's flock. Package-scoped so the descriptor
// stays open — and the lock held — for the process lifetime.
var lockFile *os.File

// acquireLock takes an exclusive, non-blocking flock on path, so only one
// bot can run per machine.
//
// The previous read-PID-then-write scheme was racy: two processes starting
// within the same window (launchd KeepAlive respawn, a second launcher, or
// the auto-update re-exec) both read a stale/absent file, both decided they
// were alone, and both connected to the same Discord token — which makes
// the gateway deliver every message twice, so every command runs twice.
// flock is atomic, so the loser fails immediately instead.
//
// The descriptor carries O_CLOEXEC (Go's default), so syscall.Exec during
// !restart / auto-update drops the lock and the replacement image re-takes
// it. Never delete the lock file to release it: a fresh file is a different
// inode, and a second instance could lock that one in parallel.
func acquireLock(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		holder := readLockPID(f)
		f.Close()
		if holder != "" {
			return fmt.Errorf("another instance is running (PID %s); stop it with `pigeon-claw stop`", holder)
		}
		return fmt.Errorf("another instance is running; stop it with `pigeon-claw stop`")
	}

	// Taking the flock is not proof we are alone: versions before the flock
	// recorded a PID here and never locked the file, so an old binary still
	// running leaves the lock free and both processes end up on the same
	// Discord token. Honour the old scheme too while such binaries can
	// still be out there.
	if holder := livePigeonClaw(readLockPID(f)); holder != "" {
		f.Close()
		return fmt.Errorf("another instance is running (PID %s, predates the file lock); "+
			"stop it with `pigeon-claw stop` or `pkill -f 'pigeon-claw serve'`", holder)
	}

	// Record the PID for humans, `pigeon-claw status`, and the check above.
	// The flock, not this content, is what enforces exclusivity among
	// current versions.
	if err := f.Truncate(0); err == nil {
		if _, err := f.Seek(0, io.SeekStart); err == nil {
			fmt.Fprint(f, os.Getpid())
		}
	}
	lockFile = f
	return nil
}

// livePigeonClaw returns pid when it names a running pigeon-claw that is
// not this process, else "".
//
// Checked against the process's actual command, not just liveness: a
// crashed instance's PID gets recycled, and refusing to start because an
// unrelated program inherited the number would be worse than the duplicate
// this guards against. Our own PID is expected here after `!restart`,
// which execs and keeps it.
func livePigeonClaw(pid string) string {
	if pid == "" || pid == strconv.Itoa(os.Getpid()) {
		return ""
	}
	n, err := strconv.Atoi(pid)
	if err != nil || n <= 0 {
		return ""
	}
	if err := syscall.Kill(n, 0); err != nil {
		return "" // gone
	}
	out, err := exec.Command("ps", "-p", pid, "-o", "command=").Output()
	if err != nil || !strings.Contains(string(out), "pigeon-claw") {
		return "" // PID recycled by something else
	}
	return pid
}

// readLockPID best-effort reads the PID recorded by the lock holder.
func readLockPID(f *os.File) string {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(f, 32))
	if err != nil {
		return ""
	}
	pid := strings.TrimSpace(string(data))
	if n, err := strconv.Atoi(pid); err != nil || n <= 0 {
		return ""
	}
	return pid
}

// releaseLock unlocks and closes the lock file. The file itself is left in
// place on purpose — see acquireLock.
func releaseLock() {
	if lockFile == nil {
		return
	}
	syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	lockFile.Close()
	lockFile = nil
}
