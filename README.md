# RunPod Orchestrator

<div align="center">

[![GitHub stars](https://img.shields.io/github/stars/Eudaeon/runpod-orchestrator?style=for-the-badge)](https://github.com/Eudaeon/runpod-orchestrator/stargazers)
[![GitHub forks](https://img.shields.io/github/forks/Eudaeon/runpod-orchestrator?style=for-the-badge)](https://github.com/Eudaeon/runpod-orchestrator/network)
[![GitHub issues](https://img.shields.io/github/issues/Eudaeon/runpod-orchestrator?style=for-the-badge)](https://github.com/Eudaeon/runpod-orchestrator/issues)
[![GitHub license](https://img.shields.io/github/license/Eudaeon/runpod-orchestrator?style=for-the-badge)](LICENSE)

**A CLI tool that spins up a RunPod GPU or CPU pod, drops you into a shell, and tears it down when you exit.**

</div>

## Overview

RunPod Orchestrator provisions a single pod on demand, connects you to it over a reverse-ssh tunnel, and terminates it the moment your shell exits. It is built for short, throwaway sessions: pick the hardware, do the work, and never leave a pod billing in the background.

Each workload is its own command. `hashcat` launches a GPU pod from the Hashcat template; `sagemath` launches a CPU pod from the SageMath template. Both follow the same flow: choose the hardware (interactively or by flag), wait for the pod to come online, optionally upload files, then hand you a shell.

**How it works:**

- **Authentication**: there is no interactive login. The tool reuses a logged-in browser session on the RunPod console to mint short-lived JWTs on demand, so no long-term API key is stored.
- **Selection**: GPUs and CPU sizes are read live from RunPod's Secure Cloud catalog. When you do not pass exact flags, an interactive picker shows current prices and stock and pre-selects the cheapest available option.
- **Connection**: the pod dials back to a relay on your machine through a public [bore](https://bore.pub/) tunnel, and you connect into that. No inbound ports, no RunPod SSH keys, and no console setup are needed.
- **Cleanup**: every exit path stops and terminates the pod, including Ctrl-C and hangups, then verifies it is gone and prints a spend summary. If termination cannot be confirmed, it warns you to remove the pod by hand.

## Setup

Go is required (see [go.mod](go.mod) for the version).

```bash
git clone https://github.com/Eudaeon/runpod-orchestrator.git
cd runpod-orchestrator
go build -o runpod-orchestrator .
```

This produces a `runpod-orchestrator` binary in the current directory. The examples below call it directly; during development you can substitute `go run .` for the binary.

## Authentication

The orchestrator does not log in for you. Instead, you copy two values once from a browser that is already signed in to [console.runpod.io](https://console.runpod.io), and it mints fresh tokens from them as needed.

The two values are:

- **`session_id`**: the Clerk session id, which looks like `sess_...`.
- **`client_cookie`**: the value of the `__client` cookie sent to `clerk.runpod.io`.

To grab them, open the RunPod console while signed in, open your browser's developer tools, and go to the **Network** tab. Find the request to:

```text
POST https://clerk.runpod.io/v1/client/sessions/<session_id>/tokens
```

The `session_id` is the segment in that URL. The `client_cookie` is the value of the `__client` cookie on `clerk.runpod.io`, which you can also read under **Application → Cookies**.

> [!IMPORTANT]
> Both values are sensitive: anyone holding them can act as your RunPod session. Keep them out of shell history and shared files. The config file is written with owner-only permissions, and the tool never prints these values back.

### Providing the credentials

The orchestrator reads credentials from three places, each overriding the one before it.

**1. Config file** (default), at `~/.config/runpod-orchestrator/config.json`:

```json
{
  "session_id": "sess_xxxxxxxxxxxxxxxxxxxxxxxxxxxx",
  "client_cookie": "..."
}
```

Use a different path with the `--config` flag.

**2. Environment variables**, which override the file:

```bash
export RUNPOD_SESSION_ID="sess_..."
export RUNPOD_CLIENT_COOKIE="..."
```

**3. Flags**, which override everything:

```bash
runpod-orchestrator hashcat --session-id sess_... --client-cookie ...
```

> [!NOTE]
> The session credentials expire when the browser session ends. If commands start failing with authentication errors, sign in to the console again and refresh the two values.

## Usage

### hashcat

Launch a GPU pod from the Hashcat template and open a shell on it:

```bash
runpod-orchestrator hashcat
```

With no flags, the GPU picker opens over the live Secure Cloud catalog. Only NVIDIA GPUs on Secure Cloud are offered. Rows are sorted by cost-efficiency and show each GPU's measured MD4 speed and Hash/$ (see [Benchmarks](#benchmarks)) next to its live price. Pick a GPU, then a count; the picker fetches each count's vCPU, RAM, and price and starts on the lowest count in stock.

The `--gpu` and `--count` flags pin either step. Pass both to deploy directly without the picker; pass just one and the picker handles the rest. With `--gpu` it opens straight on the count step for that GPU, and with `--count` it applies that count to whichever GPU you pick:

```bash
runpod-orchestrator hashcat --gpu "NVIDIA GeForce RTX 4090" --count 1
```

If a pinned GPU or count cannot be placed right now, the picker opens with an explanation rather than failing.

Any files passed as arguments are uploaded to `/workspace` once the pod is online, which is handy for hash files:

```bash
runpod-orchestrator hashcat admin.hash user.hash
```

|       Flag      |                      Description                      |       Default      |
|:---------------:|:-----------------------------------------------------:|:------------------:|
|     `--gpu`     |              NVIDIA GPU type id to deploy             | pick interactively |
| `-n`, `--count` |                Number of GPUs to deploy               | pick interactively |
|     `--cuda`    | Comma-separated allowed CUDA versions (empty for any) |     `12.8,12.9`    |
|     `--wait`    |       Minutes to wait for the pod to come online      |        `15`        |

### sagemath

Launch a CPU pod from the SageMath template (General Purpose, 5 GHz) and open a shell on it:

```bash
runpod-orchestrator sagemath
```

With no flags, the vCPU-size picker opens and starts on the cheapest size in stock. RAM scales with the size at 4 GB per vCPU. To skip the picker, pin the count:

```bash
runpod-orchestrator sagemath --cpus 8
```

As with `hashcat`, any files passed as arguments are uploaded to `/workspace` once the pod is online.

|      Flag      |                 Description                |       Default      |
|:--------------:|:------------------------------------------:|:------------------:|
| `-n`, `--cpus` |          Number of vCPUs to deploy         | pick interactively |
|    `--wait`    | Minutes to wait for the pod to come online |        `15`        |

### The session

Once the pod is online, the session runs in the terminal's alternate screen, so it does not clutter your scrollback while live. Everything you do is recorded. When you exit the shell, the alternate screen is torn down and the full transcript is replayed into your scrollback, leaving a record of the session. The relay's own reverse-ssh logs are written to a file under your cache directory rather than the terminal, so they stay out of the way.

Exiting the shell (with `exit` or Ctrl-D) terminates the pod. The tool then prints a summary:

```text
Session summary
  Duration  1m29s
  Hardware  RTX 2000 Ada (6 vCPU, 31 GB)
  Price     $0.24/hr
  Spent     ~$0.0059
  Balance   $7.50
```

Cleanup is best-effort but persistent: termination is retried and verified by listing the account's pods. If it still cannot confirm the pod is gone, it prints a warning with the pod id so you can terminate it in the [RunPod console](https://console.runpod.io) before it bills further.

## Global flags

These apply to every command and override the config file and environment.

|        Flag       |                         Description                        |
|:-----------------:|:----------------------------------------------------------:|
|     `--config`    |   Path to the config file (default: the path shown above)  |
|   `--session-id`  |     Clerk session id, overriding config and environment    |
| `--client-cookie` | `__client` cookie value, overriding config and environment |

## Benchmarks

A reference of raw MD4 hash rates across the NVIDIA Secure Cloud catalog, measured with `hashcat -b -w 4` (benchmark mode, hash-mode 900) on a single GPU (`x1`) at workload profile 4.

- **Speed** is the MD4 rate reported by hashcat, in GH/s.
- **Price/h** is the RunPod Secure Cloud on-demand rate for one GPU at the time of the run. Rates and stock change over time, so treat these as a snapshot.
- **Hash/$** is hashes per dollar of on-demand time (`Speed × 3600 ÷ Price/h`), a rough cost-efficiency measure. Higher is better.

Rows are sorted by cost-efficiency, most Hash/$ first. GPUs not yet benchmarked are listed at the end, by price; `Unknown` marks one not yet measured.

|       Name      |    Speed    | Price/h |   Hash/$   |
|:---------------:|:-----------:|:-------:|:----------:|
|     RTX 4090    |  88.99 GH/s |  $0.69  | 464.3 TH/$ |
|     RTX 5090    | 126.90 GH/s |  $0.99  | 461.5 TH/$ |
|   RTX 4000 Ada  |  27.23 GH/s |  $0.26  | 377.1 TH/$ |
|    RTX A5000    |  27.57 GH/s |  $0.27  | 367.6 TH/$ |
|   RTX 6000 Ada  |  77.54 GH/s |  $0.77  | 362.5 TH/$ |
|    RTX A4500    |  24.04 GH/s |  $0.25  | 346.2 TH/$ |
|       L40S      |  81.98 GH/s |  $0.99  | 298.1 TH/$ |
|       A40       |  34.55 GH/s |  $0.44  | 282.7 TH/$ |
|    RTX A6000    |  36.71 GH/s |  $0.49  | 269.7 TH/$ |
|        L4       |  23.31 GH/s |  $0.39  | 215.2 TH/$ |
|   RTX PRO 6000  | 123.60 GH/s |  $2.09  | 212.9 TH/$ |
|   RTX 2000 Ada  |  13.36 GH/s |  $0.24  | 200.4 TH/$ |
|    A100 PCIe    |  38.48 GH/s |  $1.39  |  99.7 TH/$ |
|     A100 SXM    |  38.85 GH/s |  $1.49  |  93.9 TH/$ |
|     H100 SXM    |  75.15 GH/s |  $3.29  |  82.2 TH/$ |
| RTX PRO 6000 WK |  41.63 GH/s |  $1.89  |  79.3 TH/$ |
|     H100 NVL    |  67.45 GH/s |  $3.19  |  76.1 TH/$ |
|     H200 SXM    |  74.87 GH/s |  $4.39  |  61.4 TH/$ |
|       B200      |  85.59 GH/s |  $5.89  |  52.3 TH/$ |
|    RTX A4000    |   Unknown   |  $0.25  |   Unknown  |
|     RTX 3090    |   Unknown   |  $0.46  |   Unknown  |
|   RTX PRO 4000  |   Unknown   |  $0.57  |   Unknown  |
|   RTX PRO 4500  |   Unknown   |  $0.74  |   Unknown  |
|       L40       |   Unknown   |  $0.82  |   Unknown  |
|   RTX PRO 5000  |   Unknown   |  $0.96  |   Unknown  |
|    H100 PCIe    |   Unknown   |  $2.89  |   Unknown  |
|     H200 NVL    |   Unknown   |  $3.79  |   Unknown  |
|       B300      |   Unknown   |  $7.39  |   Unknown  |
