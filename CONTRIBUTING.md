# Contributing

Thanks for helping. Small, focused pull requests are easiest to review.

## Workflow
- Branch off `develop` and open the PR against `develop`.
- CI runs `gofmt`, `go vet`, `go build` and `go test` on Linux, macOS and Windows. Run them locally first:
  ```sh
  gofmt -l .   # must print nothing
  go vet ./... && go test ./...
  ```
- `main` is the release branch and is updated from `develop` by the maintainer.

## Guidelines
- Keep it small: no new dependency for a few lines of code, no options nobody asked for.
- New logic with a branch or parser gets a small test.
- Never commit `.env` or any token. If you find a security problem, report it privately (see SECURITY.md).
