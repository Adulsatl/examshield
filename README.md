# ExamShield EDU 

This PoC includes a minimal backend API (Go) and a Windows-capable agent (Go). The agent enrolls with the server and sends periodic heartbeats, fetching a static policy for now.

## Structure

- `server/`
  - `main.go` – HTTP API with `/health`, `/agents/enroll`, `/agents/heartbeat`, `/config`
  - `go.mod`
- `agent/`
  - `cmd/agent/main.go` – CLI agent that enrolls and heartbeats
  - `go.mod`

## Prerequisites

- Go 1.21+
- Windows 10/11 (recommended for PoC run of the agent)

## Run Locally (Two terminals)

1. Start the API server

```powershell
# Terminal A
# Working directory: server/
go run .
```

You should see: `ExamShield EDU API listening on :8080`

2. Start the agent

```powershell
# Terminal B
# Working directory: agent/cmd/agent/
$env:EXAMSHIELD_SERVER="http://127.0.0.1:8080"; go run .
```

You should see enroll success and periodic `heartbeat ok` logs. The agent writes its state (agent ID and token) to a local state directory (on Windows: `%ProgramData%\ExamShieldEDU`).

## Next Steps

- Implement Windows process watcher and USB-block toggles in the agent.
- Add Admin Dashboard (Next.js) shell to visualize machines and events.
- Add Telegram bot webhook for `/startexam` and `/stopexam`.

## Notes

  - This PoC uses in-memory storage on the server. Restarting the server will clear enrolled agents; the agent will re-enroll automatically if token/ID are missing.
  - For production, add TLS (reverse proxy or Go TLS), JWT/mTLS auth, Postgres, and object storage for screenshots.

## Platform-Specific Instructions

### Windows

- Run API server
  - In `server/`:
    - `go run .` (if 8080 is busy, set `PORT=8081` or `PORT=8082`)
  - Health check: `curl http://127.0.0.1:8080/health`

- Run agent (interactive, for testing)
  - In `agent/cmd/agent/`:
    - PowerShell: `$env:EXAMSHIELD_SERVER="http://127.0.0.1:8080"; go run .`
  - Expected: enroll + periodic heartbeats. Try launching a blocked browser to see it terminated and an event posted.

- Install as background Windows Service (auto-start)
  - Run as Administrator: `scripts\install-windows.ps1`
  - Prompts for:
    - Server URL
    - Telegram Bot Token + Chat ID (for alerts)
    - Whitelisted app process names (e.g., `code.exe,pwsh.exe`)
  - Installs service `ExamShieldEDU` and starts it.
  - Update config later at: `%ProgramData%\ExamShieldEDU\config.json`

### Linux / Ubuntu

- Build `.deb` package (on Ubuntu/WSL/Docker)
  - Requirements: Go, `dpkg-deb`, bash
  - Commands:
    - `cd packaging/deb`
    - `./build.sh`
  - Output: `examshield-agent_0.1.0_amd64.deb`

- Install `.deb`
  - `sudo apt install ./examshield-agent_0.1.0_amd64.deb`
  - Service: `examshield-agent` (systemd)
    - `sudo systemctl status examshield-agent`
    - `sudo systemctl restart examshield-agent`

- Configure server URL (optional)
  - `sudo systemctl edit examshield-agent` and add:
    - `[Service]`
    - `Environment=EXAMSHIELD_SERVER=http://your-server:8081`
  - Then: `sudo systemctl daemon-reload && sudo systemctl restart examshield-agent`

- Linux agent status
  - Current PoC: enrolls and heartbeats. Linux USB blocking and process watcher will be added next.

### Python CLI (PyPI)

- Location: `python-sdk/`
- Build and upload (after you have a PyPI token):
  - `python -m pip install --upgrade build twine`
  - `cd python-sdk`
  - `python -m build`
  - `twine upload dist/*`
- Use (after install):
  - `pip install examshield-cli`
  - `EXAMSHIELD_SERVER=http://127.0.0.1:8082 examshield health`
  - `EXAMSHIELD_SERVER=http://127.0.0.1:8082 examshield events`

### Notes

- Windows screenshots save to `%ProgramData%\ExamShieldEDU\shot_*.png` when a blocked app is terminated.
- Linux screenshots will use PipeWire/xdg-desktop-portal in a later iteration.
