package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"sync"
	"time"

	"runpod-orchestrator/internal/binstore"
)

const (
	boreVersion = "v0.6.0"
	boreServer  = "bore.pub"
)

// bore.pub announces the assigned remote port on stdout/stderr, e.g.
//
//	listening at bore.pub:41234
//	... remote_port=41234
var borePortRe = regexp.MustCompile(`(?:bore\.pub:|remote_port=)(\d+)`)

// boreAssetTarget maps the running platform to bore's release asset triple.
func boreAssetTarget() (string, error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64":
		return "x86_64-unknown-linux-musl", nil
	case "linux/arm64":
		return "aarch64-unknown-linux-musl", nil
	case "darwin/amd64":
		return "x86_64-apple-darwin", nil
	case "darwin/arm64":
		return "aarch64-apple-darwin", nil
	default:
		return "", fmt.Errorf("bore: unsupported platform %s/%s", runtime.GOOS, runtime.GOARCH)
	}
}

// Bore exposes localPort publicly via the shared bore.pub server. It downloads
// the bore binary on first use, starts `bore local <localPort> --to bore.pub`,
// and blocks until the public port is announced (or ctx/timeout fires).
func Bore(ctx context.Context, localPort int) (*Tunnel, error) {
	target, err := boreAssetTarget()
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("https://github.com/ekzhang/bore/releases/download/%s/bore-%s-%s.tar.gz",
		boreVersion, boreVersion, target)

	bin, err := binstore.EnsureTarGz(ctx, "bore", url, "bore")
	if err != nil {
		return nil, err
	}

	// The tunnel must outlive the (short) ctx used for setup, so give the
	// process its own cancellable context.
	runCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(runCtx, bin, "local", strconv.Itoa(localPort), "--to", boreServer)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	cmd.Stderr = cmd.Stdout // bore logs to stderr; fold it into the same pipe

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("bore: start: %w", err)
	}

	portCh := make(chan int, 1)
	go scanForPort(stdout, portCh)

	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()

	select {
	case port := <-portCh:
		var once sync.Once
		closeFn := func() error {
			once.Do(cancel)
			_ = cmd.Wait()
			return nil
		}
		return &Tunnel{Host: boreServer, Port: port, closeFn: closeFn}, nil
	case <-deadline.C:
		cancel()
		_ = cmd.Wait()
		return nil, fmt.Errorf("bore: timed out waiting for public port")
	case <-ctx.Done():
		cancel()
		_ = cmd.Wait()
		return nil, ctx.Err()
	}
}

// scanForPort sends the assigned port once found, then keeps draining r so bore
// never blocks on a full output pipe during a long session.
func scanForPort(r io.Reader, out chan<- int) {
	sc := bufio.NewScanner(r)
	found := false
	for sc.Scan() {
		if found {
			continue
		}
		if m := borePortRe.FindStringSubmatch(sc.Text()); m != nil {
			if p, err := strconv.Atoi(m[1]); err == nil {
				out <- p
				found = true
			}
		}
	}
}
