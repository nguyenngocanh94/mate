# matev2 agent notes

- Spec and task list: `docs/mvp.md`. Read it before touching code.
- Module `github.com/nguyenngocanh94/matev2`. Binary `cmd/matev2`.
- `make check` runs gofmt, `go vet`, and `go test -json ./...` through `scripts/gotestreport`.
  Any skipped test not declared in `scripts/gotestreport/expected-skips.txt` fails the build.
  Live Herdr/harness tests are named `TestLive*` and skip unless `MATEV2_LIVE=1`.
  A green suite that skipped live tests is not evidence those features work.
- v1 source for copy-and-adapt: `/Volumes/Work/Workspace/mate` (module `github.com/nguyenngocanh94/mate`). firstmate reference: upstream https://github.com/kunchenguid/firstmate (the local clone at `/Volumes/Work/Workspace/firstmate` was 665 commits behind on 2026-09-24; read upstream, never pull into the user's clone).
- Env var prefix is `MATEV2_`, state dir is `.matev2/`. Never `MATE_` or `.mate/`.
- Commit messages: terse, imperative. Never add an agent name as co-author.
