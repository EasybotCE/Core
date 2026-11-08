# EasybotCE

EasybotCE is an independently developed, community-maintained Minecraft integration platform, created with authorization from the original Easybot author. It treats server instances as the primary entities and features as configurable operations. The Hub combines Minecraft servers, chat bots, and independent plugin endpoints.

The repository now contains a working Go daemon, SQLite/MySQL/PostgreSQL storage adapters, endpoint management APIs, routing rules, a WebSocket endpoint protocol, and an MC protocol simulator. **This is still an early development build, not a complete v1 release.** Account workflows, native OneBot11 adapters, commands, moderation, AI, image templates, migration, observability, and delivery packaging remain incomplete. The web UI is intentionally deferred; the management API is implemented first.

## Run locally

Requires Go 1.27. The default database is SQLite at `easybotce.db`. Generate an administrator bearer token and start the daemon from PowerShell:

```powershell
$bytes = New-Object byte[] 32
$rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
$rng.GetBytes($bytes)
$rng.Dispose()
$env:EASYBOTCE_ADMIN_TOKEN = [Convert]::ToBase64String($bytes)
go run ./cmd/hub
```

The daemon listens on `127.0.0.1:8080` by default. Verify readiness and query the authenticated API:

```powershell
Invoke-RestMethod http://127.0.0.1:8080/healthz
$headers = @{ Authorization = "Bearer $env:EASYBOTCE_ADMIN_TOKEN" }
Invoke-RestMethod http://127.0.0.1:8080/v1/system/info -Headers $headers
```

`/healthz` is a liveness check; `/readyz` checks database readiness. See [development and operation](docs/development.md) for config, database, API, simulator, test, and build instructions.

## Run with Docker

Generate a strong administrator token and start the SQLite deployment with Docker Compose. The database is stored in a named volume, and the published port binds to localhost only:

```powershell
$bytes = New-Object byte[] 32
$rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
$rng.GetBytes($bytes)
$rng.Dispose()
$env:EASYBOTCE_ADMIN_TOKEN = [Convert]::ToBase64String($bytes)
docker compose -f compose.yaml up --build -d
Invoke-RestMethod http://127.0.0.1:8080/readyz
```

For production, terminate TLS at a trusted reverse proxy and restrict network access. Do not expose the container port publicly without a secure transport and firewall policy.
## Documentation and contracts

- [Documentation index](docs/README.md)
- [Implementation status](docs/implementation-status.md), including the gap to full v1
- [Architecture](docs/architecture.md), [decisions](docs/decisions.md), and [v1 scope](docs/v1-scope.md)
- [MC/plugin WebSocket protocol](docs/protocol.md)
- [OpenAPI 3.1](api/openapi.json) and [protocol JSON Schema](api/protocol/endpoint.schema.json)
- [Contribution guide](CONTRIBUTING.md)

The MC-side plugin/MOD is built by another team. This repository provides its integration contract and simulator. The SDK is maintained separately at `github.com/EasybotCE/sdk`. OneBot support is part of the design target but is not yet implemented in the Hub.

Organization: <https://github.com/EasybotCE>. Go module: `github.com/EasybotCE/easybotce`.

## License

Hub source code is licensed under **AGPL-3.0-only**. Protocol JSON Schema and examples are MIT-licensed. See [LICENSE](LICENSE), [LICENSE-NOTICES.md](LICENSE-NOTICES.md), and [api/protocol/LICENSE](api/protocol/LICENSE).




