//go:build !linux

package flights

import "os/exec"

// hardenChromeCmd is a no-op off Linux: Pdeathsig is a Linux (and a few BSD)
// facility, and the deployment target is Linux. On other platforms the browser
// is still cleaned up by Close on the normal and panic paths.
func hardenChromeCmd(cmd *exec.Cmd) {}
