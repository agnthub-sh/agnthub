//go:build !unix

package source

import "os/exec"

// isolate keeps the default cancellation, which kills git itself; its helpers
// stop once they notice the closed pipes (cmd.WaitDelay bounds the wait).
func isolate(*exec.Cmd) {}
