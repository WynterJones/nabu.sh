//go:build !unix

package remoteaccess

import "os/exec"

func configureProcessGroup(_ *exec.Cmd) {}

func terminateProcessGroup(command *exec.Cmd) {
	if command.Process != nil {
		_ = command.Process.Kill()
	}
}
