# matev2 agent notes

- Spec and task list: `docs/mvp.md`. Read it before touching code.
- Module `github.com/nguyenngocanh94/matev2`. Binary `cmd/matev2`.
- `make check` runs gofmt, `go vet`, and `go test -json ./...` through `scripts/gotestreport`.
  Any skipped test not declared in `scripts/gotestreport/expected-skips.txt` fails the build.
  Live Herdr/harness tests are named `TestLive*` and skip unless `MATEV2_LIVE=1`.
  A green suite that skipped live tests is not evidence those features work.
- v1 source for copy-and-adapt: `/Volumes/Work/Workspace/mate` (module `github.com/nguyenngocanh94/mate`). firstmate reference: `/Volumes/Work/Workspace/firstmate`.
- Env var prefix is `MATEV2_`, state dir is `.matev2/`. Never `MATE_` or `.mate/`.
- Commit messages: terse, imperative. Never add an agent name as co-author.
