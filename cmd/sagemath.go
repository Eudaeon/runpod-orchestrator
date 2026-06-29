package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"runpod-orchestrator/internal/revssh"
	"runpod-orchestrator/internal/runpod"
	"runpod-orchestrator/internal/ui"
)

// The SageMath template on RunPod (sagemath/sagemath:latest, CPU).
const sagemathTemplateID = "2agard1lia"

// sagemathCpuFlavor is the CPU family sagemath deploys on: "General Purpose" on the
// CPU5 (5 GHz) generation. RAM scales at 4 GB per vCPU.
const sagemathCpuFlavor = "cpu5g"

// sagemathFlavorLabel is the human-facing name for sagemathCpuFlavor.
const sagemathFlavorLabel = "General Purpose (5 GHz)"

var (
	flagSagemathCPUs     int
	flagSagemathWaitMins int
)

var sagemathCmd = &cobra.Command{
	Use:   "sagemath [files...]",
	Short: "Launch a CPU pod for SageMath and drop into a shell",
	Long: "Provision a RunPod General Purpose (5 GHz) CPU pod from the SageMath\n" +
		"template, attach a reverse-ssh shell over a public tunnel, and terminate\n" +
		"the pod when the shell exits. Any files given as arguments are scp'd to\n" +
		"/workspace once the pod is online.",
	RunE: runSagemath,
}

func init() {
	f := sagemathCmd.Flags()
	f.IntVarP(&flagSagemathCPUs, "cpus", "n", 0, "number of vCPUs to deploy (default: pick interactively)")
	f.IntVar(&flagSagemathWaitMins, "wait", 15, "minutes to wait for the pod to come online")
}

func runSagemath(cmd *cobra.Command, args []string) error {
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
	// path (including these signals, since they only cancel ctx).
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	me, err := client.GetMyself(ctx)
	if err != nil {
		return err
	}
	tmpl, err := client.GetPodTemplate(ctx, sagemathTemplateID)
	if err != nil {
		return err
	}

	// Resolve the CPU offering (General Purpose, 5 GHz). By default this opens the
	// interactive size picker; an explicit --cpus deploys that size directly,
	// falling back to the picker only when it can't be placed right now.
	inst, err := resolveCpus(ctx, client, cmd.Flags().Changed("cpus"))
	if err != nil {
		return err
	}
	if inst == nil {
		fmt.Fprintf(out, "No CPU size selected.\n")
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

	fmt.Fprintf(out, "Deploying CPU %s (x%d).\n", sagemathFlavorLabel, inst.Vcpu)
	// Deploy on a context that ignores the interrupt signal: if the user hits
	// Ctrl-C mid-deploy, RunPod may still create the pod, so we must capture its
	// id no matter what — otherwise it would bill with no way to clean it up.
	deployCtx, cancelDeploy := context.WithTimeout(context.Background(), 90*time.Second)
	pod, err := client.DeployCpuPod(deployCtx, runpod.CpuDeployInput{
		Name:              "sagemath-" + randomSuffix(),
		InstanceID:        inst.InstanceID,
		TemplateID:        sagemathTemplateID,
		ImageName:         tmpl.ImageName,
		DockerArgs:        dockerArgs,
		ContainerDiskInGb: tmpl.ContainerDiskInGb,
		VolumeMountPath:   tmpl.VolumeMountPath,
		Ports:             tmpl.Ports,
		DeployCost:        inst.Price,
		StartSsh:          true,
		StartJupyter:      false,
	})
	cancelDeploy()
	if err != nil {
		return err
	}
	started := time.Now()

	// From here on, always tear the pod down and report the spend.
	defer sagemathCleanup(out, client, pod.ID, inst, started)

	// If the user interrupted during deploy, stop now; the defer cleans up.
	if ctx.Err() != nil {
		return nil
	}

	info := ui.WaitInfo{
		Workload: "sagemath",
		Balance:  me.ClientBalance,
		GPUName:  sagemathFlavorLabel,
		Cores:    inst.Vcpu,
		MemGB:    inst.RamGb,
		Price:    inst.Price,
		PodID:    pod.ID,
	}
	ready, err := ui.RunWait(ctx, client, relay.BindPort, info, time.Duration(flagSagemathWaitMins)*time.Minute)
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

// cpuSizeLadder is the standard vCPU sizes offered for a CPU pod; RAM scales
// with the flavor's multiplier (4 GB/vCPU for General Purpose).
var cpuSizeLadder = []int{2, 4, 8, 16, 32}

// resolveCpus returns the CPU offering to deploy. Unless the user pinned --cpus,
// it opens the interactive size picker. With --cpus it resolves that size
// directly and deploys it, dropping into the picker (with an explanation) only
// when RunPod can't currently place it. A nil instance (with nil error) means
// the user quit the picker.
func resolveCpus(ctx context.Context, client *runpod.Client, cpusPinned bool) (*runpod.CpuInstance, error) {
	// fetch resolves a vCPU size for our flavor, or nil when it has no stock.
	fetch := func(vcpu int) (*runpod.CpuInstance, error) {
		inst, err := client.GetCpuInstance(ctx, sagemathCpuFlavor, vcpu)
		if err != nil {
			return nil, err
		}
		if inst.StockStatus == "" || inst.Price <= 0 {
			return nil, nil
		}
		return inst, nil
	}

	notice := ""
	if cpusPinned {
		inst, err := fetch(flagSagemathCPUs)
		if err != nil {
			return nil, err
		}
		if inst != nil {
			return inst, nil
		}
		notice = fmt.Sprintf("No Secure Cloud stock for a %d-vCPU %s pod right now.", flagSagemathCPUs, sagemathFlavorLabel)
	}

	return ui.PickCpu(ctx, "CPU · "+sagemathFlavorLabel, notice, cpuSizeLadder, fetch)
}

// sagemathHardwareLabel renders the CPU offering as "<flavor> (N vCPU, M GB)".
func sagemathHardwareLabel(inst *runpod.CpuInstance) string {
	return fmt.Sprintf("%s (%d vCPU, %d GB)", sagemathFlavorLabel, inst.Vcpu, inst.RamGb)
}

// sagemathCleanup stops and terminates the pod, verifies it is gone, then prints a
// session spend summary. It uses a fresh context so it runs to completion even
// when the main context was cancelled by a signal.
func sagemathCleanup(out io.Writer, client *runpod.Client, podID string, inst *runpod.CpuInstance, started time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if err := client.StopPod(ctx, podID); err != nil && !isNotFound(err) {
		fmt.Fprintf(out, "  warning: stop failed: %v\n", err)
	}
	terminateAndVerify(ctx, out, client, podID)

	dur := time.Since(started)
	spent := dur.Hours() * inst.Price
	fmt.Fprintf(out, "Session summary\n")
	fmt.Fprintf(out, "  Duration  %s\n", dur.Round(time.Second))
	fmt.Fprintf(out, "  Hardware  %s\n", sagemathHardwareLabel(inst))
	fmt.Fprintf(out, "  Price     $%.2f/hr\n", inst.Price)
	fmt.Fprintf(out, "  Spent     ~$%.4f\n", spent)
	if me, err := client.GetMyself(ctx); err == nil {
		fmt.Fprintf(out, "  Balance   $%.2f\n", me.ClientBalance)
	}
}
