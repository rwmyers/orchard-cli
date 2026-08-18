package vcs

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// Almost every driver works by running the version control system's own command
// line tool, so the three ways orchard needs to do that live here rather than
// being written out again in each driver. Using them also keeps a third-party
// driver's output looking like the built-in one's.

var (
	outMu     sync.RWMutex
	outWriter io.Writer = os.Stdout
	errWriter io.Writer = os.Stderr
)

// SetOutput redirects where [Run] announces and streams commands. Passing nil
// for either writer discards that stream. It exists so that tests, and any
// program embedding orchard, can capture what drivers print.
func SetOutput(out, err io.Writer) {
	if out == nil {
		out = io.Discard
	}
	if err == nil {
		err = io.Discard
	}
	outMu.Lock()
	defer outMu.Unlock()
	outWriter, errWriter = out, err
}

func writers() (io.Writer, io.Writer) {
	outMu.RLock()
	defer outMu.RUnlock()
	return outWriter, errWriter
}

// Run executes a command in dir, announcing it first and streaming its output
// through, which is what makes an `orchard add` show the work it is doing. Use
// it for the commands that change something.
func Run(dir, name string, args ...string) error {
	out, errOut := writers()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = out
	cmd.Stderr = errOut
	_, _ = io.WriteString(out, "Running: "+name+" "+strings.Join(args, " ")+" (in "+dir+")\n")
	return cmd.Run()
}

// Output runs a command in dir and returns its standard output. Nothing is
// announced, so use it for the commands that only ask a question.
func Output(dir, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	return cmd.Output()
}

// Succeeds reports whether a command in dir exits zero. A non-zero exit comes
// back as false rather than an error, since that is how the command line tools
// answer the existence questions drivers ask them; anything that stops the
// command running at all is still an error.
func Succeeds(dir, name string, args ...string) (bool, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false, nil
	}
	return false, err
}
