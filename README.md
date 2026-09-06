# Dockge Updater

A lightweight update dashboard for Docker Compose stacks managed by [Dockge](https://github.com/louislam/dockge). It checks for newer container images and can apply updates manually or on a schedule.

## Features

- Discovers stacks from the Dockge stacks directory
- Checks for newer container images on a daily schedule
- Supports manual, nightly, and weekly updates
- Shows service status and recent activity in a web interface
- Runs as a single Go binary with JSON file storage

Automatic updates are disabled by default.

## Deployment

Copy `compose.yaml` to the server and create a `.env` file:

```dotenv
UPDATER_IMAGE=ghcr.io/klppl/dockge-updater:latest
UPDATER_PORT=8088
DOCKGE_STACKS_DIR=/opt/stacks
TZ=UTC
```

Start the service:

```sh
docker compose pull
docker compose up -d --no-build
```

The dashboard is available at `http://127.0.0.1:8088` by default. If the container package is private, authenticate to `ghcr.io` before pulling it.

To build the image locally instead:

```sh
docker compose up -d --build
```

## Container image

The `Package Docker image` workflow can be run manually from the GitHub Actions page. It publishes these tags to GitHub Container Registry:

```text
ghcr.io/klppl/dockge-updater:latest
ghcr.io/klppl/dockge-updater:sha-<commit>
```

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `LISTEN_ADDR` | `:8080` | Address used inside the container |
| `STACKS_DIR` | `/opt/stacks` | Directory containing Dockge stacks |
| `DATA_DIR` | `/data` | Directory used for persistent state |
| `TZ` | System timezone | Timezone used by the scheduler |
| `LOG_LEVEL` | `info` | Set to `debug` for request logging |

Check and update schedules are configured from the web interface. State is stored in `./data/state.json` when using the included Compose file.

## How it works

Checks pull the images referenced by each Compose stack and compare them with the images used by its running containers. Checks do not restart containers. Applying an update runs Docker Compose for the complete stack.

Services without an `image` reference are skipped. Image pruning is not performed automatically.

## Security

Dockge Updater has no built-in authentication. Keep it behind an authenticated reverse proxy and do not expose it directly to the internet. Access to the Docker socket gives the container administrative control over Docker on the host.

## Development

Requires Go 1.23 or newer and Docker Compose v2.

```sh
go test ./...
go run ./cmd/dockge-updater
```
