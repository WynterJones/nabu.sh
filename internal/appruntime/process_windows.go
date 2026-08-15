//go:build windows

package appruntime

import (
	"os"
	"os/exec"
)

func configureCommand(*exec.Cmd) {}

func interruptProcess(process *os.Process) error { return process.Signal(os.Interrupt) }

func killProcess(process *os.Process) error { return process.Kill() }
