import typer
from .commands import cmd_app

app = typer.Typer(
    help="A CLI tool to streamline the deployment of Sage and Hashcat instances on Runpod, with automated reverse SSH tunneling.",
    no_args_is_help=True,
    add_completion=False,
)
app.registered_commands.extend(cmd_app.registered_commands)

if __name__ == "__main__":
    app()
