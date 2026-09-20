# OpenAPI contract

`openapi.yaml` is the design-first contract for the control-plane. Handlers,
UI clients, and agent messages must be derived from these resource names and
state enums. Validate it from the repository with:

```bash
docker run --rm \
  -v "$PWD/tests/contracts:/src" \
  -v "$PWD/api:/api" \
  -w /src golang:1.23-alpine go test ./...
```
