# Contributing to compschema

Thanks for your interest in contributing!

## Getting started

```bash
git clone https://github.com/codewandler/compschema.git
cd compschema
task check   # runs fmt, vet, lint, test
```

## Development workflow

```bash
task fmt       # format code
task lint      # run golangci-lint
task test      # run tests
task check     # all of the above
task generate  # regenerate examples
task specs     # run multi-API test suite
```

## Making changes

1. Fork the repository
2. Create a branch (`git checkout -b feature/my-change`)
3. Make your changes
4. Run `task check` — must pass with 0 lint issues
5. Commit with a clear message
6. Open a pull request

## Code style

- All code must pass `golangci-lint` with the project's `.golangci.yml`
- Use `goimports` for import ordering
- Generated code files end in `.gen.go` or `.gen_test.go`
- Test files should cover the happy path and at least one error case

## Testing

The project has multiple test layers:

- **Unit tests**: `go test ./...`
- **Generated tests**: `compschema generate` produces smoke tests per type
- **Multi-API tests**: `task specs` runs against 11 real-world OpenAPI specs

## Architecture

See [docs/DESIGN.md](docs/DESIGN.md) for the project architecture, IR design, and validation strategy.

## Reporting issues

- Use GitHub Issues
- Include the OpenAPI spec or Go types that reproduce the problem
- Include the `compschema` version (`compschema --help` shows the binary)
