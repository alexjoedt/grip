# Copilot Instructions for grip

## Project Overview

`grip` is a Go CLI tool that downloads and installs single-executable binaries directly from GitHub releases. It handles platform detection, archive unpacking, and installation tracking.

## Architecture

```
cmd/main.go          → CLI entry point, dependency wiring
cmd/{install,list,remove,update}/ → Command implementations
internal/            → Core business logic (no subpackages except logger, semver)
```

**Key Components:**
- `Installer` (`installer.go`) - Orchestrates installation: fetches releases via `GitHubClient`, parses assets, delegates to `InstallAsset()`
- `Asset` (`asset.go`) - Represents a release artifact; `parseAsset()` selects correct platform binary
- `Storage` (`storage.go`) - JSON-based tracking of installed packages (`~/.grip/grip.json`)
- `Config` (`config.go`) - Runtime configuration with platform aliases for OS/arch matching
- `Workspace` (`workspace.go`) - Manages temp directories for download/unpack lifecycle
- `Unpacker` (`unpack.go`) - Extracts tar.gz, tar.bz2, zip, tar.xz, bz2 archives

**Data Flow:** `Install command → Installer.Install() → parseAsset() → InstallAsset() → Downloader → Unpacker → BinaryInstaller → Storage.Add()`

## Development Workflow

```bash
# Build and run
task build           # Builds to bin/grip
task run             # Build + execute
task test            # Run tests with coverage

# Format and verify
task tidy            # go fmt + go mod tidy
```

Build version info is injected via ldflags at build time (`version`, `build`, `date` in `cmd/main.go`).

## Code Patterns

### Dependency Injection
Dependencies are constructed in `cmd/main.go` and passed down. The `Installer` accepts interfaces (`GitHubClient`) for testability:

```go
// Interface for mocking in tests
type GitHubClient interface {
    GetLatestRelease(ctx context.Context, owner, repo string) (*github.RepositoryRelease, error)
    GetReleaseByTag(ctx context.Context, owner, repo, tag string) (*github.RepositoryRelease, error)
}
```

### Platform Matching
Asset selection uses OS/arch aliases to match varied release naming conventions:
```go
osAliases:   {"darwin": {"macos"}, "linux": {"musl"}}
archAliases: {"amd64": {"x86_64"}, "arm64": {"aarch64", "universal"}}
```
See `MatchesPlatform()` in `platform.go`.

### Error Handling
Use sentinel errors from `errors.go` (`ErrNotFound`, `ErrInvalidRepo`, etc.) and wrap with context:
```go
return fmt.Errorf("fetch release: %w", err)
```

### Logging
Use `internal/logger` package functions: `logger.Info()`, `logger.Error()`, `logger.Success()`. Info is verbose-only.

### CLI Structure
Commands register via `Command(ctx, app, deps...)` functions that append to `*cli.App`. Follow existing patterns in `cmd/install/install.go`.

## Testing

Tests use `testify/assert`, `testify/require`, and `testify/mock`. HTTP mocking via `MockRoundTripper`:
```go
type MockRoundTripper struct {
    mock.Mock
}
```
Test archives are created programmatically (see `createTestTarGz()` in `asset_test.go`).

## File Locations

- Installed binaries: `~/.grip/bin/`
- Installation database: `~/.grip/grip.json`
- Temp downloads: System temp directory

## Adding New Features

1. **New archive format**: Add unpack function to `Unpacker.unpackers` map in `unpack.go`
2. **New command**: Create `cmd/{command}/{command}.go` following `install.go` pattern
3. **Platform alias**: Add to `Config` struct in `config.go`
