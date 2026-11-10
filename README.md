# Höðrs Hoard

Self-hosted catalogue for physical and digital media (films, books, ebooks
and more). Successor to PHP4DVD, written in Go. See [docs/spec.md](docs/spec.md).

## Build and run

```sh
make build          # static binary ./hoard (CGO_ENABLED=0)
./hoard             # listens on :8080
make test           # run the tests
```

Then open http://localhost:8080/ or http://localhost:8080/healthz.

### First start

With an empty database, hoard logs a one-time setup code:

```
level=WARN msg="no users yet: open /setup in a browser ..." setup_code=K7QF-M2XA-P4WD
```

Open http://localhost:8080/, enter the code and create the first admin
account. In a container, find the code with `podman logs <container>`.

## Configuration

| Variable          | Default | Notes                              |
|-------------------|---------|------------------------------------|
| `HOARD_ADDR`      | `:8080` | Listen address, `host:port`        |
| `HOARD_LOG_LEVEL` | `info`  | `debug`, `info`, `warn` or `error` |
| `HOARD_DATA_DIR`  | `~/.local/share/hodrs-hoard` | Data folder; the SQLite database is `hoard.db` in it |

Höðrs Hoard uses SQLite only and runs on Linux and macOS.

## License

GPL-3.0
