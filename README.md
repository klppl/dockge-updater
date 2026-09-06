# Dockge Updater

A small, single-user update dashboard for Docker Compose stacks managed by Dockge. It runs as one Go process, stores its state in one JSON file, and delegates image and container operations to the Docker Compose CLI.

## What it does

- Discovers Dockge stacks from their Compose files.
- Checks every day at a configurable local time.
- Pulls each image, then compares the deployed container image ID with the pulled image ID.
- Shows updates, service details, failures, and recent activity in a responsive web dashboard.
- Applies a stack update manually with `docker compose up -d --remove-orphans`.
- Optionally applies available updates every night or on one selected weekday.
- Serializes work so two Docker operations cannot run at the same time.

Automatic updates are **off by default**. The default check time is 03:00.
On a fresh installation, the first check starts when the service starts. A restart also catches up when the latest successful check is at least 25 hours old.

## Deploy next to Dockge

The included Compose file assumes Dockge stores stacks at `/opt/stacks`. From this directory:

```sh
docker compose up -d --build
```

Open `http://127.0.0.1:8088`, or point your Cloudflare Tunnel origin at that address. The port only binds to loopback by default.

If Dockge uses a different host directory, provide the same absolute path on both sides of the mount:

```sh
DOCKGE_STACKS_DIR=/srv/dockge/stacks docker compose up -d --build
```

To change the host port or timezone, add a `.env` file beside `compose.yaml`:

```dotenv
UPDATER_PORT=8088
TZ=Europe/Stockholm
DOCKGE_STACKS_DIR=/opt/stacks
```

The timezone controls all scheduled checks and updates. State is kept in `./data/state.json`.

## Package with GitHub Actions

The manual workflow at `.github/workflows/docker.yml` builds only for `linux/amd64` and pushes the result to GitHub Container Registry. It deliberately does not install QEMU or build extra architectures.

After pushing this repository to GitHub:

1. Open **Actions → Package Docker image**.
2. Select **Run workflow**.
3. Wait for the `Build linux/amd64` job to finish.

The workflow publishes two tags:

```text
ghcr.io/<owner>/<repository>:latest
ghcr.io/<owner>/<repository>:sha-<commit>
```

The image path is converted to lowercase automatically. BuildKit’s GitHub Actions cache is reused on later runs.

On the VPS, set the published image in `.env`:

```dotenv
UPDATER_IMAGE=ghcr.io/<owner>/<repository>:latest
UPDATER_PORT=8088
TZ=Europe/Stockholm
DOCKGE_STACKS_DIR=/opt/stacks
```

Then pull and replace the container without building locally:

```sh
docker compose pull dockge-updater
docker compose up -d --no-build dockge-updater
```

For a private GHCR package, authenticate once on the VPS with a GitHub personal access token that has `read:packages`:

```sh
echo "$GHCR_TOKEN" | docker login ghcr.io --username <github-user> --password-stdin
```

You can make the package public from its package settings if you prefer pulls without registry authentication.

## How update detection works

For each stack, the service runs the equivalent of:

1. `docker compose config --format json` to read services and image references.
2. `docker compose pull` for services that use an image.
3. `docker compose ps` and `docker inspect` to compare each deployed image ID with the freshly pulled image ID.

Checks do not restart containers. They **do download newer image layers**, which uses registry bandwidth and disk space. Services that only have a `build:` section and no `image:` reference are skipped.

When an update is approved, Compose reconciles the whole stack. Relative paths continue to work because the stacks directory is mounted into the updater at the same absolute path it has on the host.

## Private registries

Public images work without extra configuration. If the host uses a private registry, make Docker credentials available inside the updater. For example, add a read-only mount to the service:

```yaml
volumes:
  - /root/.docker/config.json:/root/.docker/config.json:ro
```

Use the credential path for the host account that already pulls those images.

## Security boundary

There is intentionally no application login because the intended deployment sits behind Cloudflare Zero Trust. Keep it private: the Docker socket gives this container administrative control of Docker and is effectively root-level access to the VPS. Do not expose the dashboard directly to the public internet.

## Configuration

Runtime environment variables:

| Variable | Default | Purpose |
| --- | --- | --- |
| `LISTEN_ADDR` | `:8080` | HTTP bind address inside the container |
| `STACKS_DIR` | `/opt/stacks` | Directory containing one folder per Dockge stack |
| `DATA_DIR` | `/data` | Directory for the JSON state file |
| `TZ` | system local time | Timezone used by the scheduler |
| `LOG_LEVEL` | `info` | Set to `debug` for API request logs |

Schedule settings are changed from the web interface.

## Local development

Go 1.23 or newer is sufficient for the application itself. Docker with the Compose v2 plugin must be available when starting the server.

```sh
go test ./...
go run ./cmd/dockge-updater
```

For local Dockge data, override `STACKS_DIR` and `DATA_DIR`.

## Operational notes

- Only one check or update job runs at once.
- Failed stacks do not stop the remaining stacks from being checked.
- The activity log keeps the latest 80 events.
- Old image layers are not deleted automatically. Pruning can remove rollback options, so keep that as a separate, deliberate host maintenance task.
- Stateful applications should have tested backups before automatic updates are enabled.
