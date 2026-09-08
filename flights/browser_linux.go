//go:build linux

package flights

import (
	"os/exec"
	"syscall"
)

// hardenChromeCmd asks the kernel to send SIGKILL to the Chrome child the
// moment this process dies, by any means — a normal exit, a panic, a
// subprocess timeout, or an outright SIGKILL that never gives Go a chance to
// run Close/defer. Without this a killed or timed-out gflights invocation
// strands its browser (reparented to init, PPID 1), and a caller looping the
// CLI accumulates orphaned Chromes until the machine runs out of memory.
func hardenChromeCmd(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}
