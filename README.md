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

## Configuration

| Variable          | Default | Notes                              |
|-------------------|---------|------------------------------------|
| `HOARD_ADDR`      | `:8080` | Listen address, `host:port`        |
| `HOARD_LOG_LEVEL` | `info`  | `debug`, `info`, `warn` or `error` |
| `HOARD_DATA_DIR`  | `~/.local/share/hodrs-hoard` | Data folder; the SQLite database is `hoard.db` in it |

Höðrs Hoard uses SQLite only and runs on Linux and macOS.

## License

GPL-3.0
