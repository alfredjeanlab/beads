# Beads

An event-driven work-tracking system. Manages hierarchical work items ("beads") with dependencies, labels, comments, and event history. Exposes gRPC and REST APIs and ships a Cobra-based CLI (`bd`).

## Quick start

```sh
go build ./cmd/bd

export BEADS_DATABASE_URL="postgres://user:pass@localhost:5432/beads?sslmode=disable"
bd serve
```

Or via Docker:

```sh
docker build -t bd .
docker run -e BEADS_DATABASE_URL="..." -p 9090:9090 -p 8080:8080 bd
```

## CLI examples

```sh
bd create "Fix login bug" --type bug
bd list --status open
bd show bd-abc123
bd update bd-abc123 --status in_progress
bd close bd-abc123
bd comment bd-abc123 "Root cause was a nil pointer"
bd label bd-abc123 add backend
bd dep bd-abc123 add bd-def456
bd search "login"
```

Custom types can be registered at runtime:

```sh
bd config create type:decision '{"kind":"issue","fields":[{"name":"outcome","type":"enum","values":["approved","rejected","pending"],"required":true}]}'
bd create "Approve Q1 roadmap" --type decision --fields '{"outcome":"pending"}'
```

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `BEADS_DATABASE_URL` | *(required)* | Postgres connection string |
| `BEADS_GRPC_ADDR` | `:9090` | gRPC listen address |
| `BEADS_HTTP_ADDR` | `:8080` | HTTP listen address |
| `BEADS_NATS_URL` | *(optional)* | NATS event bus URL |
| `BEADS_SERVER` | `localhost:9090` | CLI client target address |

## Testing

```sh
go test ./...    # uses go-sqlmock; no running Postgres needed
```

## License

See [LICENSE](LICENSE) for details.
