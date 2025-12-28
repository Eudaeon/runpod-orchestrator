import typer
from .commands import cmd_app

app = typer.Typer(
    help="Runpod Orchestrator", no_args_is_help=True, add_completion=False
)
app.registered_commands.extend(cmd_app.registered_commands)

if __name__ == "__main__":
    app()
