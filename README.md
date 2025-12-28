# Runpod Orchestrator

A CLI tool to deploy and manage Sage and Hashcat instances on Runpod with automated reverse SSH tunneling.

## Features

- Handles pod creation, connection establishment, and automatic termination upon exit.
- Injects a payload into the pod to bypass network restrictions and establish a secure connection back to your VPS.
- Automatically `scp` local files to the pod's `/workspace` directory during deployment.
- Validates GPU models and core counts against Runpod's available inventory before deployment.

## Setup

This tool is designed to run on a VPS with a public IP. It performs the following actions:

1. Starts a local web server to serve the reverse SSH payload to the pod.
2. Listens for incoming reverse SSH connections.
3. Proxies an interactive SSH session to the pod.

Ensure the binary `upx_reverse-sshx64` is located at `/workspace/tooling/` on your VPS. [This binary](https://github.com/Fahrj/reverse-ssh?tab=readme-ov-file#build-tricks) should be built with public key access.

Then, install this tool with:

```bash
pip install git+https://github.com/Eudaeon/runpod-orchestrator.git
```

## Configuration

The orchestrator requires several environment variables to be set.

| Variable | Description |
| --- | --- |
| `RUNPOD_SESSION_ID` | Your Runpod Clerk session ID. Check requests to `https://clerk.runpod.io/v1/client/sessions/<ID>/tokens` to find this value. |
| `RUNPOD_CLERK_COOKIE` | The `__client` cookie value from Runpod. Check the value of the `__client` cookie in requests to `https://clerk.runpod.io/v1/client/sessions/<ID>/tokens` to find this value. |
| `VPS_IP` | The public IP of your VPS for the reverse connection. Used to setup the reverse tunneling and SSH access. |

## Usage

### Sage (CPU)

Deploys a CPU-based SageMath instance:

```bash
runpod-orchestrator sage [FILES...]
```

### Hashcat (GPU)

Deploys a GPU-based Hashcat instance. You can specify the GPU model and core count (default: 1x NVIDIA A40):

```bash
runpod-orchestrator hashcat [FILES...] --gpu "NVIDIA RTX 3090" --cores 2
```
