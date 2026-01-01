# Runpod Orchestrator

<div align="center">

[![GitHub stars](https://img.shields.io/github/stars/Eudaeon/runpod-orchestrator?style=for-the-badge)](https://github.com/Eudaeon/runpod-orchestrator/stargazers)
[![GitHub forks](https://img.shields.io/github/forks/Eudaeon/runpod-orchestrator?style=for-the-badge)](https://github.com/Eudaeon/runpod-orchestrator/network)
[![GitHub issues](https://img.shields.io/github/issues/Eudaeon/runpod-orchestrator?style=for-the-badge)](https://github.com/Eudaeon/runpod-orchestrator/issues)
[![GitHub license](https://img.shields.io/github/license/Eudaeon/runpod-orchestrator?style=for-the-badge)](LICENSE)

**A CLI tool to streamline the deployment of Sage and Hashcat instances on Runpod, with automated reverse SSH tunneling.**

</div>

## 📖 Overview

This CLI tool is designed to simplify the interaction with the Runpod cloud platform for specific compute-intensive tasks. It enables users to effortlessly deploy containers running SageMath or Hashcat, directly from their terminal.

This tool interacts with the Runpod API using GraphQL queries. It gets a fresh JWT token for every request, using the provided Clerk cookie and session ID. It relies on the [`reverse-ssh`](https://github.com/Fahrj/reverse-ssh) utility to establish a stable, interactive SSH shell: this is achieved by hosting the binary on a local web server, starting it in listening mode, and appending commands to the template Docker start arguments to download and execute this binary.

This tool automatically terminates the rented instances on exit to ensure optimal cost efficiency.

## 📦 Setup

### Infrastructure

> [!IMPORTANT]
> This tool must be run from a VPS with a public IP. It operates by starting a local web server to serve binaries and listening for incoming reverse SSH connections.

- **Filesystem**: Ensure the [`reverse-ssh`](https://github.com/Fahrj/reverse-ssh) binary is located at `/workspace/tooling/upx_reverse-sshx64` on your VPS. The binary should be built with public key access, and the corresponding private key must be present on the VPS.
- **Networking**: The orchestrator opens a local web server on port 80 and a listener on port 8889, so your firewall should accept connections to these ports.

### Installation

```bash
pip install git+https://github.com/Eudaeon/runpod-orchestrator.git
```

## 🔧 Usage

### SageMath (CPU)

Deploys a CPU-based SageMath instance using the `cpu5c-2-4` ("Compute-Optimized") instance type.

```bash
runpod-orchestrator sage [FILES...]
```

**Arguments:**

- `FILES` (Optional) - List of local files to automatically `scp` to the pod's `/workspace/` directory during deployment.

### Hashcat (GPU)

Deploys a GPU-based Hashcat instance with automated hardware validation.

```bash
runpod-orchestrator hashcat [FILES...] [options]
```

**Arguments:**

- `FILES` (Optional) - List of local files to automatically `scp` to the pod's `/workspace/` directory during deployment.

**Options:**

- `--gpu <text>` - GPU Model to request (default: "RTX 4090").
- `--cores <integer>` - Number of GPUs to provision (default: 1).

The tool validates your requested core count against Runpod's available inventory before deployment to ensure the configuration is supported.

## ⚙️ Configuration

### Environment Variables

The orchestrator requires the following variables to be set in your environment:

<div align="center">

|        Variable       |                      Description                      |
|:---------------------:|:-----------------------------------------------------:|
|  `RUNPOD_SESSION_ID`  |             Your Runpod Clerk session ID.             |
| `RUNPOD_CLERK_COOKIE` |     The `__client` cookie value from Runpod Clerk.    |
|        `VPS_IP`       | The public IP of your VPS for the reverse connection. |

</div>

> [!TIP]
> To find your `RUNPOD_SESSION_ID` and `RUNPOD_CLERK_COOKIE`, inspect network requests to `clerk.runpod.io` in your browser's developer tools.

## 📊 Benchmarks

### Hashcat (GPU)

Benchmarks for cracking MD4 hashes (`hashcat -b -w 4`) on secure cloud instances:

<div align="center">

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

</div>

---

<div align="center">

**⭐ Star this repo if you find it helpful!**

Made with ❤️ by [Eudaeon](https://github.com/Eudaeon)

</div>