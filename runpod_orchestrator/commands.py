import typer
import asyncio
import sys
from typing import List, Optional
from rich.table import Table
from pwnlogger import logger
from .client import RunpodClient
from .core import orchestrate
from .config import SAGE_TEMPLATE_ID, HASHCAT_TEMPLATE_ID

cmd_app = typer.Typer()


@cmd_app.command()
def sage(files: Optional[List[str]] = typer.Argument(None)):
    """Deploys a CPU-based Sage instance."""
    asyncio.run(orchestrate(SAGE_TEMPLATE_ID, "cpu5c-2-4", "sage", files))


@cmd_app.command()
def hashcat(
    files: Optional[List[str]] = typer.Argument(None),
    gpu: str = typer.Option("RTX 4090", "--gpu"),
    cores: int = typer.Option(1, "--cores"),
):
    """Deploys a GPU-based Hashcat instance with hardware validation."""

    async def run_with_validation():
        try:
            async with RunpodClient() as client:
                nvidia_gpus = await client.get_nvidia_gpus()
                selected_gpu = next(
                    (g for g in nvidia_gpus if g["displayName"].lower() == gpu.lower()),
                    None,
                )

                is_valid_cores = (
                    selected_gpu
                    and selected_gpu["minPodGpuCount"]
                    <= cores
                    <= selected_gpu["maxGpuCountSecureCloud"]
                )

                if not selected_gpu or not is_valid_cores:
                    error_msg = (
                        f"Invalid GPU: '{gpu}'"
                        if not selected_gpu
                        else f"Invalid Cores: {cores} for {gpu}"
                    )
                    logger.error(error_msg)

                    table = Table(title="Available Nvidia GPUs")
                    table.add_column("GPU Model", style="cyan")
                    table.add_column("Min Cores", justify="center")
                    table.add_column("Max Cores", justify="center")
                    table.add_column("Price/Hr", style="green")

                    for g in nvidia_gpus:
                        table.add_row(
                            g["displayName"],
                            str(g["minPodGpuCount"]),
                            str(g["maxGpuCountSecureCloud"]),
                            f"${g['securePrice']}",
                        )

                    logger.debug(table)
                    sys.exit(1)

                await orchestrate(
                    HASHCAT_TEMPLATE_ID,
                    "",
                    "hashcat",
                    files,
                    True,
                    selected_gpu["id"],
                    cores,
                )
        except Exception as e:
            logger.error(e)
            sys.exit(1)

    asyncio.run(run_with_validation())
