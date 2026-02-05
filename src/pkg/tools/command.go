package tools

import (
	"bytes"
	"os/exec"
	"strings"
)

const defaultShellCommand = "/bin/bash"

// ShellProcess is the Go equivalent of the Python ShellProcess.
// It wraps shell command execution and always returns a string output.
type ShellProcess struct {
	Command         string
	StripNewlines   bool
	ReturnErrOutput bool
}

// NewShellProcess mirrors the Python __init__ defaults.
func NewShellProcess(command string, stripNewlines, returnErrOutput bool) *ShellProcess {
	if command == "" {
		command = defaultShellCommand
	}
	return &ShellProcess{
		Command:         command,
		StripNewlines:   stripNewlines,
		ReturnErrOutput: returnErrOutput,
	}
}

// Run is equivalent to ShellProcess.run(...):
// - accepts one or more commands
// - joins them with ';'
// - ensures the resulting string starts with sp.Command
// - then delegates to Exec.
func (sp *ShellProcess) Run(args []string, input []byte) string {
	if len(args) == 0 {
		return ""
	}

	commands := strings.Join(args, ";")
	if !strings.HasPrefix(commands, sp.Command) {
		commands = strings.TrimSpace(sp.Command + " " + commands)
	}

	return sp.execString(commands, input)
}

// RunString is a convenience wrapper for the common "single string" case.
func (sp *ShellProcess) RunString(arg string, input []byte) string {
	if arg == "" {
		return ""
	}
	return sp.Run([]string{arg}, input)
}

// Exec is equivalent to ShellProcess.exec(...):
// - accepts commands as list
// - joins with ';'
// - executes via /bin/sh -c "<commands>"
// - combines stdout+stderr
// - returns a string regardless of success/failure.
func (sp *ShellProcess) Exec(commands []string, input []byte) string {
	if len(commands) == 0 {
		return ""
	}
	return sp.execString(strings.Join(commands, ";"), input)
}

// ExecString is a convenience wrapper for a single string.
func (sp *ShellProcess) ExecString(commands string, input []byte) string {
	if commands == "" {
		return ""
	}
	return sp.execString(commands, input)
}

// internal implementation: mirrors subprocess.run(..., shell=True, stdout=PIPE, stderr=STDOUT)
func (sp *ShellProcess) execString(commands string, input []byte) string {
	// Python's shell=True uses /bin/sh -c.
	cmd := exec.Command("/bin/sh", "-c", commands)

	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}

	// CombinedOutput == stdout + stderr (like stdout=PIPE, stderr=STDOUT)
	out, err := cmd.CombinedOutput()

	// Match Python semantics: always return a string, even on failure.
	if err != nil {
		if sp.ReturnErrOutput {
			s := string(out)
			if sp.StripNewlines {
				s = strings.TrimSpace(s)
			}
			return s
		}
		s := err.Error()
		if sp.StripNewlines {
			s = strings.TrimSpace(s)
		}
		return s
	}

	s := string(out)
	if sp.StripNewlines {
		s = strings.TrimSpace(s)
	}
	return s
}
