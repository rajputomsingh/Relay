# Deployment Guide

This guide covers running Relay in a production environment using Docker and Docker Compose.

## Prerequisites

- Docker Engine v24.0+
- Docker Compose v2.20+
- A reachable MongoDB 7 instance (or use the included Compose service)

## Configuration

Relay reads its configuration entirely from environment variables. In production, these should be injected via your orchestrator (Kubernetes Secrets, AWS Secrets Manager, etc.) or an `.env` file passed to Docker Compose.

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `APP_ENV` | No | `development` | Set to `production` to enable production safeguards (e.g., rejecting HTTP webhook URLs). |
| `PORT` | No | `8080` | The port the HTTP server listens on. |
| `MONGODB_URI` | **Yes** | - | Connection string for MongoDB. |
| `MONGODB_DATABASE` | No | `relay` | The database name to use. |
| `RELAY_API_KEY` | **Yes** | - | The bearer token required to access the API. |
| `RELAY_WORKER_POLL_SECONDS` | No | `2` | How often workers poll MongoDB for pending deliveries. |
| `RELAY_MAX_DELIVERY_ATTEMPTS` | No | `8` | Maximum retry attempts for a failed delivery. |
| `RELAY_WORKER_CONCURRENCY` | No | `4` | Number of parallel delivery workers (1-32). |
| `RELAY_WEBHOOK_SIGNING_SECRET` | No | - | If set, signs outbound webhooks with HMAC-SHA256 (min 32 chars). |

## Running with Docker Compose

The fastest way to deploy is using the provided `docker-compose.yml`. This starts both Relay and a MongoDB 7 container.

1. Create a `.env` file in the root directory:
```bash
RELAY_API_KEY=your-secure-production-key
```

2. Build and start the services:
```bash
make docker-up
# Or: docker compose up -d --build
```

3. Verify the service is healthy:
```bash
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
```

## Security Hardening

The provided Dockerfile uses a **distroless** base image. This image contains no shell and no package manager, drastically reducing the attack surface. It runs as the non-root user `nonroot`.

Ensure that:
1. `RELAY_API_KEY` is generated with sufficient entropy (e.g., `openssl rand -hex 32`).
2. MongoDB is not exposed to the public internet.
3. `RELAY_WEBHOOK_SIGNING_SECRET` is stored securely and rotated periodically.

## Operations

- **Liveness:** Use `GET /healthz`. If this fails, restart the container.
- **Readiness:** Use `GET /readyz`. If this fails, Relay cannot connect to MongoDB and should not receive traffic.
- **Logs:** Relay outputs structured JSON logs to stdout. Use `docker logs relay-app -f` or your log aggregator to view them.

## Troubleshooting

The commands below assume you run them from the repository root, where `docker-compose.yml` lives.

### MongoDB connection fails or `readyz` returns 503

**Symptom:** Relay exits at startup or stays up but `GET /readyz` returns `503` with `{"status":"not_ready"}`. Logs contain `failed to ping mongo` or `failed to initialize mongo client`.

**Likely cause:** The connection string is malformed, MongoDB is not reachable, or credentials contain characters that must be percent-encoded.

The MongoDB driver parses `MONGODB_URI` as a standard connection string. If the username or password contains reserved characters such as `@`, `:`, `/`, `?`, `#`, or `%`, the URI is split incorrectly. For example, the password `p@ss:w/rd` must be encoded as `p%40ss%3Aw%2Frd`:

```text
mongodb://relay_user:p%40ss%3Aw%2Frd@db.example.com:27017/relay?authSource=admin
```

Note that the included `docker-compose.yml` sets `MONGODB_URI=mongodb://mongo:27017` inside the Relay container. Editing `MONGODB_URI` in `.env` has no effect on that container because the Compose `environment` block takes precedence. To point the container at an external MongoDB, change the `MONGODB_URI` value in `docker-compose.yml` itself.

**Resolution:**
- Percent-encode reserved characters in the username and password before building the URI. On most systems you can use:

  ```bash
  python3 -c "import urllib.parse; print(urllib.parse.quote('p@ss:w/rd', safe=''))"
  ```

- If you need credentials for the bundled MongoDB, note that the Compose service currently starts without authentication; the URI `mongodb://mongo:27017` is correct for that setup.
- When Relay runs in a container, `localhost` refers to the container itself, not your host. Use the Compose service name (`mongo`) or a reachable host address.

**Diagnostic commands:**

```bash
docker compose ps
docker compose logs relay
docker compose logs mongo
curl -i http://localhost:8080/readyz
docker compose exec mongo mongosh --quiet --eval 'db.runCommand({ ping: 1 })'
```

### Port already in use

**Symptom:** `docker compose up` fails with an error such as `Bind for 0.0.0.0:8080 failed: port is already allocated`, or `27017` is already in use.

**Likely cause:** Another process, container, or a locally installed MongoDB is already listening on the port that Compose wants to publish (`8080` for Relay, `27017` for MongoDB).

**Resolution:** Stop the conflicting process, or change the *host* side of the port mapping in `docker-compose.yml`. For example, to publish Relay on host port `8081` while keeping it on `8080` inside the container:

```yaml
ports:
  - "8081:8080"
```

Only the left-hand value needs to change when the container port stays the same. If you also change the application's `PORT`, update both sides of the mapping and the `PORT` environment variable. MongoDB's container port can stay `27017` because Relay reaches it over the Compose network at `mongo:27017`.

**Diagnostic commands:**

```bash
docker compose ps
docker ps --format 'table {{.Names}}\t{{.Ports}}'
# Linux / macOS
lsof -i :8080
lsof -i :27017
# Windows (PowerShell or cmd)
netstat -ano | findstr :8080
netstat -ano | findstr :27017
```

On Windows, take the PID from `netstat` and identify the process with `tasklist /FI "PID eq <pid>"`.

### Volume mounting problems on Windows

**Symptom:** MongoDB keeps restarting, data does not persist, or the monitoring containers fail to start because a mounted file cannot be read. Common messages include `not a directory`, `permission denied`, or Grafana provisioning errors.

**Likely cause:** Docker Desktop on Windows can only bind-mount files from drives shared with the Docker VM. The monitoring stack bind-mounts `prometheus.yml`, `grafana-dashboard.json`, and `grafana-datasource.yml` from the repo, so the repo must live on a shared drive. Windows line endings (CRLF) in the YAML files can also break provisioning.

**Resolution:**
- In Docker Desktop, enable file sharing for the drive that contains the repository, then restart Docker Desktop.
- Clone the repository under a shared location such as `C:\Users\<name>\Relay` and run `docker compose` from the repository root so the relative bind-mount paths resolve.
- If a YAML file was edited on Windows, ensure it uses LF line endings before restarting the monitoring stack.
- The `mongo_data` volume is a named volume managed by Docker, not a bind mount. If it was created by an incompatible MongoDB version and MongoDB exits on startup, you may need to reset it. See [Resetting MongoDB data](#resetting-mongodb-data-destructive).

**Diagnostic commands:**

```bash
docker compose ps
docker compose logs mongo
docker volume ls
docker volume inspect relay_mongo_data
```

### Resetting MongoDB data (destructive)

> **Warning:** The command below permanently deletes the `mongo_data` volume and all stored MongoDB data. This cannot be undone. Only run it when you intend to reset the database.

```bash
docker compose down -v
```

### Relay container exits or cannot reach MongoDB

**Symptom:** `relay-app` restarts repeatedly or stops immediately after `docker compose up`. Logs show `invalid configuration`.

**Likely cause:** Configuration validation failed. Common cases:
- `RELAY_API_KEY must contain at least 32 characters`. The fallback in `docker-compose.yml` is `${RELAY_API_KEY:-dev-secret-api-key}`, which is shorter than 32 characters, so Compose starts but Relay exits unless you provide a key of at least 32 characters in `.env`.
- `MONGODB_URI is required` or `PORT must be between 1 and 65535`.
- Relay cannot reach `mongo` because the MongoDB container is unhealthy or still starting.

**Resolution:**
- Generate a key with `openssl rand -hex 32`, then copy the output into the repository root `.env` as `RELAY_API_KEY=<generated-value>`. Compose reads this file to substitute `${RELAY_API_KEY}`.
- Wait for the MongoDB healthcheck to pass. The `relay` service depends on `mongo` being healthy, but a failing healthcheck prevents Relay from starting at all.
- Remember that the Relay image is distroless and has no shell, so `docker compose exec relay sh` will fail. Use `docker compose logs relay` and host-side `curl` for diagnosis.

**Diagnostic commands:**

```bash
docker compose ps
docker compose logs relay
docker compose logs mongo
docker compose config
docker inspect --format '{{.State.Health.Status}}' relay-mongo
curl -i http://localhost:8080/healthz
curl -i http://localhost:8080/readyz
```

### Prometheus cannot scrape Relay metrics

**Symptom:** Relay is healthy, but in Prometheus the `relay` target under **Status > Targets** shows as down with an authentication error such as `401 Unauthorized`.

**Likely cause:** The `/metrics` endpoint requires Bearer authentication, and the credential Prometheus sends must match Relay's `RELAY_API_KEY`. `docker-compose.yml` takes the key from the `RELAY_API_KEY` environment variable (via `.env`), while `prometheus.yml` stores its own credential under `scrape_configs[].authorization.credentials`. These are two separate values, so changing one without the other breaks scraping.

**Resolution:**
- Keep the two values identical: the `RELAY_API_KEY` in `.env` must match the `credentials` value in `prometheus.yml`. When you rotate one, update the other in the same change.
- `prometheus.yml` ships with a development placeholder under `authorization.credentials`; it is already a valid 32-character value. Either set `RELAY_API_KEY` in `.env` to that same value, or replace both with your own 32+ character secret. There is no automatic link between the two files, so update them together.
- Relay rejects keys shorter than 32 characters, so the Compose fallback `dev-secret-api-key` cannot be used.
- Also ensure Relay is reachable from the Prometheus container at `host.docker.internal:8080`. If it is not, the target will show as down even when the credential matches.
- After editing either file, restart the affected services: `docker compose up -d relay` for the app and `docker compose -f docker-compose.monitoring.yml up -d prometheus` for the scraper.

**Diagnostic commands:**

```bash
docker compose -f docker-compose.monitoring.yml logs prometheus
curl -i -H "Authorization: Bearer <your-relay-api-key>" http://localhost:8080/metrics
```

Then verify the target under Prometheus **Status > Targets**.

