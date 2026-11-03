//go:build unix

package source

import (
	"os/exec"
	"syscall"
)

// isolate runs cmd in its own process group and makes cancelling it kill the
// whole group, so an abandoned fetch's transport helper and index-pack stop
// receiving with it instead of finishing the download in the background.
func isolate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
