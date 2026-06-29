// Package revssh manages the local end of a reverse-ssh session
// (https://github.com/Fahrj/reverse-ssh): it runs the listening relay, knows how
// a remote pod should dial back, and drops the user into the resulting shell.
//
// Topology (reverse scenario):
//
//	relay (local):  reverse-ssh -l -p <ListenPort>
//	pod (remote):   reverse-ssh -p <publicPort> -b <BindPort> <publicHost>
//	user (local):   ssh -p <BindPort> reverse@127.0.0.1
//
// The pod dials the relay through a public tunnel; once connected it asks the
// relay to bind BindPort locally, which forwards into the pod's shell.
package revssh

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"
	"syscall"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
)

// Alternate-screen control sequences. Entering switches to a fresh screen buffer
// (like less/vim) so the live session never touches the normal scrollback;
// leaving restores it. After switching we clear the buffer and home the cursor
// so the session starts at the top of the screen rather than wherever the
// orchestrator's output had left the cursor.
const (
	enterAltScreen = "\x1b[?1049h\x1b[2J\x1b[H"
	exitAltScreen  = "\x1b[?1049l"
)

// ReleaseVersion is the reverse-ssh release used for both the local relay and
// the binary the pod downloads.
const ReleaseVersion = "v1.2.0"

// VictimURL is where the pod downloads the reverse-ssh client (linux/amd64).
const VictimURL = "https://github.com/Fahrj/reverse-ssh/releases/download/" + ReleaseVersion + "/reverse-sshx64"

// id_reverse-ssh is the private key matching reverse-ssh's baked-in default
// authorized key, so the local ssh client can authenticate to the pod's shell
// without a password prompt.
//
//go:embed assets/id_reverse-ssh
var privateKey []byte

// Relay is a running local reverse-ssh listener.
type Relay struct {
	ListenPort int // port the relay listens on for the pod's dial-home (tunnelled publicly)
	BindPort   int // port the relay binds locally once the pod connects; the user's ssh target

	keyPath string
	cmd     *exec.Cmd
	cancel  context.CancelFunc
}

// localBinaryAsset returns the reverse-ssh release asset for the local platform.
func localBinaryAsset() (string, error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64":
		return "reverse-sshx64", nil
	case "linux/386":
		return "reverse-sshx86", nil
	case "linux/arm64":
		return "reverse-ssh-armv8-x64", nil
	case "linux/arm":
		return "reverse-ssh-armv7-x86", nil
	case "windows/amd64":
		return "reverse-sshx64.exe", nil
	default:
		return "", fmt.Errorf("revssh: unsupported local platform %s/%s", runtime.GOOS, runtime.GOARCH)
	}
}

// StartRelay downloads the reverse-ssh binary if needed and starts the relay in
// listening mode. ListenPort and BindPort are chosen automatically from free
// local ports. The relay's own (verbose) logs are written to logW; pass
// io.Discard or a file to keep them off the terminal.
func StartRelay(ctx context.Context, logW io.Writer) (*Relay, error) {
	bin, err := ensureLocalBinary(ctx)
	if err != nil {
		return nil, err
	}

	listenPort, err := freePort()
	if err != nil {
		return nil, err
	}
	bindPort, err := freePort()
	if err != nil {
		return nil, err
	}

	keyPath, err := writeKey()
	if err != nil {
		return nil, err
	}

	runCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(runCtx, bin, "-v", "-l", "-p", strconv.Itoa(listenPort))
	cmd.Stdout = logW
	cmd.Stderr = logW
	if err := cmd.Start(); err != nil {
		cancel()
		os.Remove(keyPath)
		return nil, fmt.Errorf("revssh: starting relay: %w", err)
	}

	return &Relay{
		ListenPort: listenPort,
		BindPort:   bindPort,
		keyPath:    keyPath,
		cmd:        cmd,
		cancel:     cancel,
	}, nil
}

// PodCommand returns a shell snippet that downloads reverse-ssh on the pod and
// dials back to the relay through the given public endpoint. It loops so a
// dropped connection reconnects and the container stays alive.
func (r *Relay) PodCommand(publicHost string, publicPort int) string {
	return fmt.Sprintf(
		"wget -q -O /tmp/rssh %s || curl -fsSL %s -o /tmp/rssh; "+
			"chmod +x /tmp/rssh; "+
			"while true; do /tmp/rssh -p %d -b %d %s; sleep 5; done",
		VictimURL, VictimURL, publicPort, r.BindPort, publicHost,
	)
}

// Connect opens an interactive ssh session into the pod via the relay's bind
// port and blocks until the shell exits. It uses the embedded key, so no
// password is required.
//
// The live session runs in the terminal's alternate screen, so it leaves the
// normal scrollback untouched, and the whole session — prompt, the keystrokes
// the pod echoes back, and command output — is recorded. When the shell exits,
// the alternate screen is torn down and the recorded transcript is replayed into
// the normal scrollback, leaving a record of what happened in the session.
func (r *Relay) Connect(ctx context.Context) error {
	args := []string{
		"-i", r.keyPath,
		"-p", strconv.Itoa(r.BindPort),
		"-tt", // force a pty so we always get an interactive shell
		"-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"reverse@127.0.0.1",
	}
	cmd := exec.CommandContext(ctx, "ssh", args...)

	// Without a real terminal on both ends (e.g. piped I/O), skip the alternate
	// screen, input gating and recording and just pass ssh straight through.
	stdinFd := os.Stdin.Fd()
	if !term.IsTerminal(stdinFd) || !term.IsTerminal(os.Stdout.Fd()) {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return shellExit(cmd.Run())
	}

	// Live session in the alternate screen, output teed into a transcript buffer.
	var transcript bytes.Buffer
	io.WriteString(os.Stdout, enterAltScreen)
	err := runSession(cmd, stdinFd, &transcript)
	io.WriteString(os.Stdout, exitAltScreen)

	// Back on the normal screen: replay the session so it lands in scrollback.
	replaySession(os.Stdout, transcript.Bytes())
	return err
}

// CopyFiles uploads local files to the pod with scp, over the relay's bind
// port, placing them in destDir (e.g. "/workspace/"). It uses the same embedded
// key and host as Connect, so no password is required.
//
// It first tries modern scp (which uses the sftp subsystem). reverse-ssh is a
// minimal server that may not offer sftp, so on failure it retries with -O, the
// legacy SCP/exec transfer protocol. The first attempt's output is buffered and
// only surfaced if the retry also fails, so a successful fallback stays quiet.
func (r *Relay) CopyFiles(ctx context.Context, files []string, destDir string) error {
	if len(files) == 0 {
		return nil
	}
	base := []string{
		"-i", r.keyPath,
		"-P", strconv.Itoa(r.BindPort), // scp spells the port -P (uppercase)
		"-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
	}
	target := "reverse@127.0.0.1:" + destDir

	scpArgs := func(extra ...string) []string {
		args := append([]string{}, base...)
		args = append(args, extra...)
		args = append(args, files...)
		return append(args, target)
	}

	// Modern (sftp) attempt: buffer its output so a clean fallback is silent.
	var buf bytes.Buffer
	first := exec.CommandContext(ctx, "scp", scpArgs()...)
	first.Stdout, first.Stderr = &buf, &buf
	if err := first.Run(); err == nil {
		os.Stderr.Write(buf.Bytes())
		return nil
	}

	// Legacy (-O) fallback for servers without an sftp subsystem.
	retry := exec.CommandContext(ctx, "scp", scpArgs("-O")...)
	retry.Stdout, retry.Stderr = os.Stderr, os.Stderr
	if err := retry.Run(); err != nil {
		os.Stderr.Write(buf.Bytes()) // include the first attempt's diagnostics
		return fmt.Errorf("revssh: scp upload failed: %w", err)
	}
	return nil
}

// runSession runs ssh on a local pty so it gets a real terminal (raw mode,
// window size, key handling all behave natively), copying it to/from the
// terminal and teeing its output into rec. Until the remote produces its first
// output — i.e. before the prompt appears — input is gated: every keystroke is
// dropped except Ctrl-C and Ctrl-D, so impatient arrow-key/Enter mashing during
// the connect handshake doesn't queue up and dump ahead of the prompt.
func runSession(cmd *exec.Cmd, stdinFd uintptr, rec io.Writer) error {
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("revssh: starting pty: %w", err)
	}
	defer ptmx.Close()

	// Keep the pty sized to the terminal, now and on every resize.
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	go func() {
		for range winch {
			_ = pty.InheritSize(os.Stdin, ptmx)
		}
	}()
	winch <- syscall.SIGWINCH // initial sizing

	// Raw mode so keystrokes pass straight through to the remote shell.
	oldState, err := term.MakeRaw(stdinFd)
	if err != nil {
		return fmt.Errorf("revssh: raw mode: %w", err)
	}
	defer term.Restore(stdinFd, oldState)

	// terminal -> pty, gated until the prompt is up. The input goroutine may
	// block on Read after the shell exits; it is abandoned as the process tears
	// the session down and exits.
	var ready atomic.Bool
	go gateInput(os.Stdin, ptmx, &ready)

	// pty -> terminal + transcript. The first output byte lifts the input gate.
	// When ssh exits, reading the pty returns EIO on Linux rather than a clean
	// EOF; that is the normal end of a session, so the copy error is ignored.
	_, _ = io.Copy(io.MultiWriter(os.Stdout, rec, readyOnWrite{&ready}), ptmx)

	return shellExit(cmd.Wait())
}

// gateInput copies in -> out. While ready is false it forwards only Ctrl-C
// (0x03) and Ctrl-D (0x04) and drops everything else; once ready it forwards
// every byte unchanged.
func gateInput(in io.Reader, out io.Writer, ready *atomic.Bool) {
	buf := make([]byte, 4096)
	for {
		n, err := in.Read(buf)
		if n > 0 {
			b := buf[:n]
			if !ready.Load() {
				b = keepInterrupts(b)
			}
			if len(b) > 0 {
				if _, werr := out.Write(b); werr != nil {
					return
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// keepInterrupts returns only the Ctrl-C / Ctrl-D bytes of p, filtered in place.
func keepInterrupts(p []byte) []byte {
	kept := p[:0]
	for _, b := range p {
		if b == 0x03 || b == 0x04 { // ETX (Ctrl-C), EOT (Ctrl-D)
			kept = append(kept, b)
		}
	}
	return kept
}

// readyOnWrite flips a flag to true on the first non-empty write — used to lift
// the input gate as soon as the remote shell produces output.
type readyOnWrite struct{ ready *atomic.Bool }

func (w readyOnWrite) Write(p []byte) (int, error) {
	if len(p) > 0 {
		w.ready.Store(true)
	}
	return len(p), nil
}

// shellExit maps a non-zero remote shell exit (e.g. logout after a failed
// command) to nil — it ends the session normally rather than signalling an
// orchestration error.
func shellExit(err error) error {
	if _, ok := err.(*exec.ExitError); ok {
		return nil
	}
	return err
}

// replaySession writes the recorded session transcript to w between start/end
// marker lines. The transcript itself is replayed verbatim (preserving the
// shell's colours and layout), with exactly one trailing newline so the end
// marker sits on its own line without a blank gap.
func replaySession(w io.Writer, data []byte) {
	if len(data) == 0 {
		return
	}
	io.WriteString(w, "--- Start of session transcript ---\n")
	w.Write(data)
	if data[len(data)-1] != '\n' {
		io.WriteString(w, "\n")
	}
	io.WriteString(w, "--- End of session transcript ---\n")
}

// Close stops the relay and removes the temporary key file.
func (r *Relay) Close() error {
	if r == nil {
		return nil
	}
	if r.cancel != nil {
		r.cancel()
	}
	if r.cmd != nil {
		_ = r.cmd.Wait()
	}
	if r.keyPath != "" {
		_ = os.Remove(r.keyPath)
	}
	return nil
}

// writeKey writes the embedded private key to a 0600 temp file for `ssh -i`.
func writeKey() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	} else {
		dir = filepath.Join(dir, "runpod-orchestrator")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
	}
	path := filepath.Join(dir, "id_reverse-ssh")
	if err := os.WriteFile(path, privateKey, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// freePort asks the OS for an available TCP port.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
