import json
import os
from typing import Optional

import requests
import typer

app = typer.Typer(add_completion=False)

def server_url() -> str:
    return os.environ.get("EXAMSHIELD_SERVER", "http://127.0.0.1:8082")

@app.command()
def health(url: Optional[str] = typer.Option(None, help="Server URL, overrides EXAMSHIELD_SERVER")):
    target = url or server_url()
    r = requests.get(f"{target}/health", timeout=5)
    typer.echo(r.text)

@app.command()
def events(url: Optional[str] = typer.Option(None, help="Server URL, overrides EXAMSHIELD_SERVER")):
    target = url or server_url()
    r = requests.get(f"{target}/events", timeout=10)
    r.raise_for_status()
    typer.echo(json.dumps(r.json(), indent=2))

@app.command()
def start_exam(url: Optional[str] = typer.Option(None, help="Server URL, overrides EXAMSHIELD_SERVER")):
    # Placeholder for future: create/start session via API
    target = url or server_url()
    typer.echo(f"Would call {target}/sessions to start exam (not implemented in PoC)")

if __name__ == "__main__":
    app()
