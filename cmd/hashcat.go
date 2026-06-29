package cmd

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"runpod-orchestrator/internal/revssh"
	"runpod-orchestrator/internal/runpod"
	"runpod-orchestrator/internal/tunnel"
	"runpod-orchestrator/internal/ui"
)

// The Hashcat template on RunPod (dizcza/docker-hashcat:cuda).
const hashcatTemplateID = "6f211pvy7k"

var (
	flagGPU      string
	flagCount    int
	flagCuda     string
	flagWaitMins int
)

var hashcatCmd = &cobra.Command{
	Use:   "hashcat [files...]",
	Short: "Launch a GPU pod for hashcat and drop into a shell",
	Long: "Provision a RunPod GPU pod from the Hashcat template, attach a reverse-ssh\n" +
		"shell over a public tunnel, and terminate the pod when the shell exits.\n" +
		"Any files given as arguments are scp'd to /workspace once the pod is online.",
	RunE: runHashcat,
}

func init() {
	f := hashcatCmd.Flags()
	f.StringVar(&flagGPU, "gpu", "", "NVIDIA GPU type id to deploy (default: pick interactively)")
	f.IntVarP(&flagCount, "count", "n", 0, "number of GPUs to deploy (default: pick interactively)")
	f.StringVar(&flagCuda, "cuda", "12.8,12.9", "comma-separated allowed CUDA versions (empty to allow any)")
	f.IntVar(&flagWaitMins, "wait", 15, "minutes to wait for the pod to come online")
}

func runHashcat(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()

	// Validate any files to upload up front, so a bad path fails before we spin
	// up (and bill) a pod.
	uploads, err := validateUploads(args)
	if err != nil {
		return err
	}

	client, err := newClient()
	if err != nil {
		return err
	}

	// Cancel the setup/wait phases on Ctrl-C / SIGTERM / terminal hangup; the pod
	// is still cleaned up via the deferred cleanup, which runs on every return
	// path (including these signals, since they only cancel ctx rather than
	// killing the process).
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	// Resolve account, template, and GPU before showing the UI.
	me, err := client.GetMyself(ctx)
	if err != nil {
		return err
	}
	tmpl, err := client.GetPodTemplate(ctx, hashcatTemplateID)
	if err != nil {
		return err
	}
	cudaVersions := splitCSV(flagCuda)

	// Resolve the GPU/count to deploy. By default this opens the interactive
	// picker; deploying directly (scriptable) requires both --gpu and --count, and
	// still falls back to the picker when that GPU/count can't be satisfied.
	gpu, gpuCount, err := resolveGpu(ctx, client, cudaVersions, cmd.Flags().Changed("gpu"), cmd.Flags().Changed("count"))
	if err != nil {
		return err
	}
	if gpu == nil {
		fmt.Fprintf(out, "No GPU selected.\n")
		return nil
	}

	// Local reverse-ssh relay (its verbose logs go to a file, not the terminal).
	relayLog := openRelayLog()
	defer relayLog.Close()
	relay, err := revssh.StartRelay(ctx, relayLog)
	if err != nil {
		return err
	}
	defer relay.Close()

	// Public tunnel (bore) to the relay's listen port.
	tun, err := startTunnel(ctx, relay.ListenPort)
	if err != nil {
		return err
	}
	defer tun.Close()

	// Patch the template's start command to dial back. RunPod execs dockerArgs as
	// argv (no implicit shell), so the multi-command script must be wrapped in an
	// explicit `bash -c '...'` or the first word is treated as the program.
	script := tmpl.DockerArgs + "; " + relay.PodCommand(tun.Host, tun.Port)
	dockerArgs := "bash -c " + shellQuote(script)

	fmt.Fprintf(out, "Deploying GPU %s (x%d).\n", gpu.DisplayName, gpuCount)
	// Deploy on a context that ignores the interrupt signal: if the user hits
	// Ctrl-C mid-deploy, RunPod may still create the pod, so we must capture its
	// id no matter what — otherwise it would bill with no way to clean it up.
	deployCtx, cancelDeploy := context.WithTimeout(context.Background(), 90*time.Second)
	pod, err := client.DeployOnDemand(deployCtx, runpod.DeployInput{
		Name:                "hashcat-" + randomSuffix(),
		ImageName:           tmpl.ImageName,
		DockerArgs:          dockerArgs,
		GpuTypeID:           gpu.ID,
		GpuCount:            gpuCount,
		MinVcpuCount:        gpu.MinVcpu,
		MinMemoryInGb:       gpu.MinMemoryInGb,
		ContainerDiskInGb:   tmpl.ContainerDiskInGb,
		VolumeInGb:          tmpl.VolumeInGb,
		VolumeMountPath:     tmpl.VolumeMountPath,
		Ports:               tmpl.Ports,
		DeployCost:          gpu.OnDemandPrice,
		StartSsh:            false,
		StartJupyter:        false,
		AllowedCudaVersions: cudaVersions,
	})
	cancelDeploy()
	if err != nil {
		return err
	}
	started := time.Now()

	// From here on, always tear the pod down and report the spend.
	defer cleanup(out, client, pod.ID, gpu, gpuCount, started)

	// If the user interrupted during deploy, stop now; the defer cleans up.
	if ctx.Err() != nil {
		return nil
	}

	// Waiting UI: shows balance / hardware / cores and streams system then
	// container logs until the pod dials home.
	info := ui.WaitInfo{
		Workload: "hashcat",
		Balance:  me.ClientBalance,
		GPUName:  deployLabel(gpu.DisplayName, gpuCount),
		Cores:    gpu.MinVcpu,
		MemGB:    gpu.MinMemoryInGb,
		Price:    gpu.OnDemandPrice,
		PodID:    pod.ID,
	}
	ready, err := ui.RunWait(ctx, client, relay.BindPort, info, time.Duration(flagWaitMins)*time.Minute)
	if err != nil {
		dumpPodLogs(client, pod.ID, out)
		return err
	}
	if !ready || ctx.Err() != nil {
		fmt.Fprintf(out, "Aborted before the pod came online.\n")
		return nil
	}

	// Upload any requested files to /workspace before opening the shell.
	if len(uploads) > 0 {
		fmt.Fprintf(out, "Copying %d file(s) to /workspace...\n", len(uploads))
		if err := relay.CopyFiles(context.Background(), uploads, "/workspace/"); err != nil {
			fmt.Fprintf(out, "  warning: %v\n", err)
		}
	}

	// Interactive shell. Use a background context so the signal-cancelled ctx
	// above doesn't tear down the session; ssh forwards Ctrl-C to the pod.
	return relay.Connect(context.Background())
}

// isNvidiaGPU reports whether a RunPod GPU type id refers to an NVIDIA card.
// RunPod ids are manufacturer-prefixed (e.g. "NVIDIA GeForce RTX 4090",
// "AMD Instinct MI300X").
func isNvidiaGPU(id string) bool {
	return strings.Contains(strings.ToUpper(id), "NVIDIA")
}

// resolveGpu returns the offering to deploy (priced for the chosen GPU count)
// plus that count. On RunPod, vCPU/RAM/price are all determined by the GPU
// count, so --count is the real lever.
//
// The flags drive how much of the picker is shown:
//   - neither --gpu nor --count: full picker (choose GPU, then count).
//   - --count only: picker for the GPU, then the pinned count is applied.
//   - --gpu only: picker jumps straight to the count phase for that GPU.
//   - both: validated and deployed directly, no picker.
//
// Whenever a pinned value can't be satisfied, the full picker opens with an
// explanation. A nil GPU (with nil error) means the user quit the picker.
func resolveGpu(ctx context.Context, client *runpod.Client, cudaVersions []string, gpuPinned, countPinned bool) (*runpod.GpuType, int, error) {
	// A pinned --count is honored even when the picker opens (e.g. no --gpu): the
	// picker uses it for the chosen GPU instead of asking again.
	pinnedCount := 0
	if countPinned {
		pinnedCount = flagCount
	}

	// Without a pinned --gpu, open the full picker; a pinned count is applied to
	// whichever GPU the user chooses.
	if !gpuPinned {
		return pickGpu(ctx, client, cudaVersions, "", pinnedCount, false)
	}

	// Validate the pinned --gpu. The count is validated too only when it was
	// pinned; otherwise the picker's count phase chooses it.
	notice := ""
	switch {
	case !isNvidiaGPU(flagGPU):
		notice = fmt.Sprintf("Only NVIDIA GPUs are supported; %q is not one.", flagGPU)
	case countPinned && flagCount < 1:
		notice = fmt.Sprintf("--count must be at least 1 (got %d).", flagCount)
	default:
		// Price at the pinned count, or a single GPU when no count was given.
		count := 1
		if countPinned {
			count = flagCount
		}
		gpu, err := client.GetGpuType(ctx, flagGPU, count, 0, cudaVersions)
		if err != nil {
			return nil, 0, err
		}
		switch {
		case gpu == nil:
			notice = fmt.Sprintf("%q is not a known RunPod GPU type.", flagGPU)
		case !gpu.SecureCloud:
			notice = fmt.Sprintf("%s is community-cloud only, not a Secure Cloud pod.", gpu.DisplayName)
		case countPinned && gpu.MaxGpuCountSecure > 0 && flagCount > gpu.MaxGpuCountSecure:
			notice = fmt.Sprintf("%s supports at most %d GPU(s) in Secure Cloud (asked for %d).", gpu.DisplayName, gpu.MaxGpuCountSecure, flagCount)
		case countPinned && (gpu.StockStatus == "" || gpu.OnDemandPrice <= 0):
			notice = fmt.Sprintf("%s has no Secure Cloud stock for %d GPU(s) on CUDA %v right now.", gpu.DisplayName, flagCount, cudaVersions)
		case countPinned:
			// GPU and count both valid: deploy directly, no picker.
			return gpu, flagCount, nil
		default:
			// GPU valid, count not given: jump straight to its count phase.
			return pickGpu(ctx, client, cudaVersions, "", 0, true)
		}
	}

	return pickGpu(ctx, client, cudaVersions, notice, pinnedCount, false)
}

// pickGpu shows the interactive picker over the live Secure Cloud catalog. The
// user picks a GPU then a count; the picker fetches each count's vCPU/RAM/price
// from RunPod. When pinnedCount > 0 the picker skips the count step and deploys
// that many GPUs of the chosen type when available. When jumpToCount is set it
// opens directly on the count phase for the preferred (--gpu) GPU. A nil GPU
// means the user quit.
func pickGpu(ctx context.Context, client *runpod.Client, cudaVersions []string, notice string, pinnedCount int, jumpToCount bool) (*runpod.GpuType, int, error) {
	all, err := client.ListGpuTypes(ctx, 1, 0, nil)
	if err != nil {
		return nil, 0, err
	}
	secure := make([]runpod.GpuType, 0, len(all))
	for _, g := range all {
		if g.IsNvidia() && g.SecureCloud {
			secure = append(secure, g)
		}
	}

	// fetchCount resolves a GPU at the given count for our CUDA filter, or nil
	// when RunPod can't place that count right now.
	fetchCount := func(gpuID string, count int) (*runpod.GpuType, error) {
		gpu, err := client.GetGpuType(ctx, gpuID, count, 0, cudaVersions)
		if err != nil {
			return nil, err
		}
		if gpu == nil || gpu.StockStatus == "" || gpu.OnDemandPrice <= 0 {
			return nil, nil
		}
		return gpu, nil
	}

	return ui.PickGpu(ctx, secure, notice, flagGPU, pinnedCount, jumpToCount, fetchCount)
}

// validateUploads checks each path exists and is a regular file, returning the
// absolute paths to scp to the pod. It runs before deploy so a bad path fails
// fast rather than after a pod has been billed.
func validateUploads(paths []string) ([]string, error) {
	files := make([]string, 0, len(paths))
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("upload %q: %w", p, err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("upload %q is a directory; only files are supported", p)
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, fmt.Errorf("upload %q: %w", p, err)
		}
		files = append(files, abs)
	}
	return files, nil
}

// deployLabel renders a GPU name with its count, e.g. "2 × NVIDIA H100".
func deployLabel(name string, count int) string {
	if count > 1 {
		return fmt.Sprintf("%d × %s", count, name)
	}
	return name
}

// startTunnel opens a public bore tunnel forwarding to localPort, giving the pod
// a publicly reachable endpoint to dial the relay through.
func startTunnel(ctx context.Context, localPort int) (*tunnel.Tunnel, error) {
	return tunnel.Bore(ctx, localPort)
}

// cleanup stops and terminates the pod, verifies it is gone, then prints a
// session spend summary. It uses a fresh context so it runs to completion even
// when the main context was cancelled by a signal.
func cleanup(out io.Writer, client *runpod.Client, podID string, gpu *runpod.GpuType, gpuCount int, started time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if err := client.StopPod(ctx, podID); err != nil && !isNotFound(err) {
		fmt.Fprintf(out, "  warning: stop failed: %v\n", err)
	}
	terminateAndVerify(ctx, out, client, podID)

	dur := time.Since(started)
	spent := dur.Hours() * gpu.OnDemandPrice
	fmt.Fprintf(out, "Session summary\n")
	fmt.Fprintf(out, "  Duration  %s\n", dur.Round(time.Second))
	fmt.Fprintf(out, "  Hardware  %s (%d vCPU, %d GB)\n", deployLabel(gpu.DisplayName, gpuCount), gpu.MinVcpu, gpu.MinMemoryInGb)
	fmt.Fprintf(out, "  Price     $%.2f/hr\n", gpu.OnDemandPrice)
	fmt.Fprintf(out, "  Spent     ~$%.4f\n", spent)
	if me, err := client.GetMyself(ctx); err == nil {
		fmt.Fprintf(out, "  Balance   $%.2f\n", me.ClientBalance)
	}
}

// terminateAndVerify terminates the pod and confirms, by listing all pods, that
// it no longer appears. It retries because termination is processed
// asynchronously. As a last resort it tells the user to remove it manually.
func terminateAndVerify(ctx context.Context, out io.Writer, client *runpod.Client, podID string) {
	const attempts = 6
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := client.TerminatePod(ctx, podID); err != nil && !isNotFound(err) {
			fmt.Fprintf(out, "  terminate attempt %d/%d failed: %v\n", attempt, attempts, err)
		}

		pods, err := client.ListPods(ctx)
		if err != nil {
			fmt.Fprintf(out, "  warning: could not list pods to verify: %v\n", err)
		} else if !containsPod(pods, podID) {
			fmt.Fprintf(out, "Pod terminated.\n")
			return
		}

		if attempt < attempts {
			time.Sleep(2 * time.Second)
		}
	}
	fmt.Fprintf(out, "  WARNING: pod %s still appears after %d attempts — terminate it manually in the RunPod console to stop billing!\n", podID, attempts)
}

func containsPod(pods []runpod.PodInfo, id string) bool {
	for _, p := range pods {
		if p.ID == id {
			return true
		}
	}
	return false
}

func isNotFound(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "not found")
}

// openRelayLog returns a writer for the relay's logs, falling back to discard.
func openRelayLog() io.WriteCloser {
	dir, err := os.UserCacheDir()
	if err != nil {
		return nopWriteCloser{io.Discard}
	}
	dir = filepath.Join(dir, "runpod-orchestrator")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nopWriteCloser{io.Discard}
	}
	f, err := os.Create(filepath.Join(dir, "relay.log"))
	if err != nil {
		return nopWriteCloser{io.Discard}
	}
	return f
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// dumpPodLogs prints the tail of a pod's logs for debugging a failed startup.
func dumpPodLogs(client *runpod.Client, podID string, out io.Writer) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	logs, err := client.PodLogs(ctx, podID)
	if err != nil {
		fmt.Fprintf(out, "  (could not fetch pod logs: %v)\n", err)
		return
	}
	fmt.Fprintf(out, "--- last container logs ---\n")
	for _, l := range tail(logs.Container, 30) {
		fmt.Fprintf(out, "  %s\n", l)
	}
	if len(logs.Container) == 0 {
		fmt.Fprintf(out, "  (no container output) --- system logs ---\n")
		for _, l := range tail(logs.System, 15) {
			fmt.Fprintf(out, "  %s\n", l)
		}
	}
}

func tail(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// shellQuote wraps s in single quotes for safe use as one `bash -c` argument.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// splitCSV splits a comma-separated flag value into trimmed, non-empty parts.
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func randomSuffix() string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 6)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}
