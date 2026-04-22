# Security Policy

## Reporting a vulnerability

If you discover a security vulnerability, please report it responsibly by emailing **codewandler@users.noreply.github.com** instead of opening a public issue.

## Scope

compschema is a code generation tool that runs at build time. It does not handle user input at runtime. The primary security considerations are:

- **Generated code safety**: the generated `Validate` and `Decode` functions use `santhosh-tekuri/jsonschema/v6` for validation — any vulnerabilities there affect compschema users.
- **OpenAPI spec parsing**: the `extract` command parses untrusted OpenAPI specs via `pb33f/libopenapi`. Malicious specs could theoretically cause excessive resource consumption.
- **Code injection**: the `import` command generates Go source code from JSON Schema. Schema values (descriptions, enum values) are sanitized before embedding in Go source, but novel injection vectors should be reported.
