# Runpod Orchestrator

An asynchronous CLI tool to deploy and manage SageMath and Hashcat instances on Runpod with automated reverse SSH tunneling.

## Features

- Handles pod creation, connection establishment, and automatic termination upon exit to ensure you only pay for what you use.
- Injects a payload into the pod to bypass network restrictions and establish a secure connection back to your VPS.
- Automatically `scp` local files to the pod's `/workspace` directory during deployment.
- Validates GPU models and core counts against Runpod's available inventory before deployment.
- Provides an interactive shell that drops you directly into a proxied SSH session once the tunnel is established.

## Setup

### Infrastructure

> [!IMPORTANT]
> This tool must be run from a VPS with a public IP. It operates by starting a local web server to serve payloads and listening for incoming reverse SSH connections.

Ensure the [`reverse-ssh`](https://github.com/Fahrj/reverse-ssh) binary is located at `/workspace/tooling/upx_reverse-sshx64` on your VPS. It should be built with public key access, with the corresponding private key on the VPS.

### Installation

Install directly from the source:

```bash
pip install git+https://github.com/Eudaeon/runpod-orchestrator.git
```

### Configuration

The orchestrator requires the following environment variables to be set:

|        Variable       |                      Description                      |
|:---------------------:|:-----------------------------------------------------:|
|  `RUNPOD_SESSION_ID`  |             Your Runpod Clerk session ID.             |
| `RUNPOD_CLERK_COOKIE` |     The `__client` cookie value from Runpod Clerk.    |
|        `VPS_IP`       | The public IP of your VPS for the reverse connection. |

> [!TIP]
> To find your `RUNPOD_SESSION_ID` and `RUNPOD_CLERK_COOKIE`, inspect network requests to `clerk.runpod.io` in your browser's developer tools.

## Usage

### Sage (CPU)

Deploys a CPU-based SageMath instance:

```bash
runpod-orchestrator sage [FILES...]
```

### Hashcat (GPU)

Deploys a GPU-based Hashcat instance. Defaults to 1x RTX 4090 if no options are provided:

```bash
runpod-orchestrator hashcat [FILES...] --gpu "RTX A4000" --cores 2
```

## Benchmarks

### Hashcat (GPU)

Benchmarks for cracking MD4 hashes (`hashcat -b -w 4`) on secure cloud instances:

|       Name      |    Speed    | Price/h |    Hash/$   |
|:---------------:|:-----------:|:-------:|:-----------:|
|    A100 PCIe    |  38.55 GH/s |  $1.39  |  27.73 GH/$ |
|     A100 SXM    |  38.77 GH/s |  $1.49  |  26.02 GH/$ |
|       A40       |  35.01 GH/s |   $0.4  |  87.53 GH/$ |
|       B200      |  85.98 GH/s |  $5.19  |  16.57 GH/$ |
|     RTX 3090    |  37.25 GH/s |  $0.46  |  80.98 GH/$ |
|     RTX 4090    |  86.48 GH/s |  $0.59  | 146.58 GH/$ |
|     RTX 5090    | 126.90 GH/s |  $0.89  | 142.58 GH/$ |
|     H100 SXM    |  74.57 GH/s |  $2.69  |  27.72 GH/$ |
|     H100 NVL    |  67.11 GH/s |  $3.07  |  21.86 GH/$ |
|    H100 PCIe    |  57.15 GH/s |  $2.39  |  23.91 GH/$ |
|     H200 SXM    |  74.84 GH/s |  $3.59  |  20.85 GH/$ |
| NVIDIA H200 NVL |  67.50 GH/s |  $3.39  |  19.91 GH/$ |
|        L4       |  24.29 GH/s |  $0.39  |  62.28 GH/$ |
|       L40       |  73.75 GH/s |  $0.99  |  74.49 GH/$ |
|       L40S      |  83.24 GH/s |  $0.86  |  96.79 GH/$ |
|   RTX 2000 Ada  |  13.50 GH/s |  $0.24  |  56.25 GH/$ |
|   RTX 4000 Ada  |  26.98 GH/s |  $0.26  | 103.77 GH/$ |
|   RTX 6000 Ada  |  82.49 GH/s |  $0.77  | 107.13 GH/$ |
|    RTX A4000    |  19.76 GH/s |  $0.25  |  79.04 GH/$ |
|    RTX A4500    |  24.63 GH/s |  $0.25  |  98.52 GH/$ |
|    RTX A5000    |  26.58 GH/s |  $0.27  |  98.44 GH/$ |
|    RTX A6000    |  37.42 GH/s |  $0.49  |  76.37 GH/$ |
|   RTX PRO 6000  | 121.80 GH/s |  $1.84  |  66.20 GH/$ |
| RTX PRO 6000 WK | 140.60 GH/s |  $2.09  |  67.27 GH/$ |
