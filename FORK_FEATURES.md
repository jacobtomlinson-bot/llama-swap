# jacobtomlinson-bot/llama-swap fork

## Container

A push to this fork's `main` runs **Fork latest image**. It builds the
checked-out fork revision with `make native` (including the web UI) and smoke
tests the image in a read-only job. A fresh publisher downloads that image by
immutable artifact ID from the same run, checks its image ID, revision, and
platform, and publishes a `sha-<commit>` tag to
`ghcr.io/jacobtomlinson-bot/llama-swap` before promoting its digest to
`:latest`. Only the publisher gets registry write credentials; it neither
checks out the repository nor runs the image. This isolates credentials, not
the trustworthiness of the built image. It is a Linux/amd64 CPU image pinned
to a llama.cpp server base digest; review and update that pin when upgrading
the bundled server. It includes a small example config that can be replaced
by a bind mount at `/app/config.yaml`. The image does not bundle models or GPU
backends; the example downloads its model on first use. The GitHub package
may initially be private: authenticate to pull it, or have the package owner
separately choose its visibility and Actions access.

Only this fork's `main` can publish this tag. Publication jobs are serialized
and compare their source SHA to live `main` before loading the artifact and
again before promoting `:latest`. The build also skips work if its SHA is
already stale. Superseded builds can leave a revision tag, but cannot
overwrite a newer job's published `:latest`. Git and GHCR don't share an
atomic transaction: `:latest` can lag a push while the new build runs, and
continues to point to the last good build on failure. Use the revision label or
pin a digest when the exact revision matters. The inherited upstream scheduled
container builds, issue cleanup, release, and chart publishing jobs skip this
fork; their behavior upstream is unchanged.

## Per-model Prometheus metrics

Scrape the existing authenticated `GET /metrics` endpoint. Set the same API
key if one is required for other inference endpoints. It still returns 503
when `performance.disabled: true`; system/GPU metrics are unchanged.

| Gauge | Meaning |
| --- | --- |
| `llamaswap_model_time_to_first_token_seconds` | Proxy-observed time from dispatch to the first nonempty streamed text delta |
| `llamaswap_model_prefill_tokens_per_second` | Normalized upstream prompt-processing rate (uncached prompt tokens per prefill second) |
| `llamaswap_model_generation_tokens_per_second` | Normalized upstream decode rate (generated tokens per generation second) |

Each gauge shows the **last available observation** per canonical `model`
label, not a mean, percentile, or total. Local aliases (including the
`/upstream/<alias>/...` path) and unambiguous peer shorthand contribute to
the canonical model, not separate series. A request without a measurement
doesn't overwrite an existing value. Gauges update when HTTP 200 generation
requests finish without an observed client cancellation. The latest TTFT and
rates may come from different requests, may remain unchanged indefinitely, and
reset on a config reload that replaces the server or a restart.

TTFT is a proxy observation, not a backend benchmark or network round trip for
the client. It includes work after dispatch (including any model startup,
upstream queuing and network delay); it excludes earlier request middleware.
It is measured at the proxy writer and uses the first recognizable, complete
single-line JSON text delta in an uncompressed SSE stream. Role-only, loading,
reasoning-only, tool-only and usage-only frames don't count. Buffered output,
compressed or unrecognized streams and WebSocket audio do not yield TTFT.
Rates require the underlying server to supply timings or rates recognized by
the existing usage parser. Token counts alone cannot establish either rate.
No estimate from total HTTP duration is substituted.

## Nightly upstream rebase

**Fork nightly upstream sync** runs at 03:27 UTC on the fork's default branch;
it can also be dispatched manually on `main`. GitHub can disable scheduled
workflows in a public repository after 60 days of repository inactivity;
check the workflow's status in Actions and
[re-enable it](https://docs.github.com/en/actions/how-tos/manage-workflow-runs/disable-and-enable-workflows#enabling-a-workflow)
there if needed.

A read-only preparation job fetches upstream `main` and rebases the fork
snapshot. If already current, it does nothing. Otherwise, before any candidate
code runs, it uploads an immutable Git bundle and records its artifact ID and
candidate SHA. A separate read-only job verifies the bundle, checks out that
SHA, and runs the Go race suite, utility and UI tests, and native build.
Only after tests pass, a fresh job with `contents: write` verifies the same
preparation outputs and bundle without executing the rebased code. It checks
upstream did not move, then updates only the fork's `main` with a lease on the
starting SHA. Tests do not select what gets pushed. This binds the update to
one commit, not the trustworthiness of upstream code or its tests. If another
push wins, the lease fails; run again from fresh refs rather than overriding it.

Git automatically combines nonoverlapping edits and identical changes when
its normal three-way rebase can do so cleanly; those candidates still need to
pass tests. If Git reports any unresolved conflict, automation stops and a
maintainer must resolve it on a review branch. There is no automatic
resolution for overlapping changes Git cannot combine, and the job does not
pick one side, apply `rerere`, or publish a partially resolved branch. A
conflict, test failure, workflow-file change, or fork lease failure leaves the
previous fork `main` untouched by the job. A successful token-authenticated update does **not**
fire push workflows; a separate job with `actions: write` dispatches **Fork
latest image** on the current fork `main`. If dispatch fails after the update,
rerun that failed job or dispatch the image workflow manually on `main`.
Image and sync artifacts expire after one day. If an artifact needed by a
failed-job rerun has expired, dispatch a fresh workflow on the current
`main`.

The sync uses the repository `GITHUB_TOKEN` only. It intentionally stops
when `.github/workflows/` changes: workflow-changing pushes may require
workflow-write authorization unavailable to the default token, and incoming
workflow changes warrant manual review anyway. GitHub branch rules must
permit the bot's leased update; rewritten fork commits lose their previous
signatures and this runner cannot sign on a maintainer's behalf. Don't weaken
rules to get a run through: fall back to a manually reviewed, signed rebase.
Use a maintainer's normal Git identity with the necessary repository and
workflow-write access. Review any upstream force-push or history rewrite
manually before syncing; the fixed original base guard only detects rewrites
that remove that base.

### Resolve a stopped sync

1. On a clean checkout verify `origin` is this fork and `upstream` is
   `mostlygeek/llama-swap`. Run `git fetch --no-tags origin main` and
   `git fetch --no-tags upstream main`. Record `old=$(git rev-parse origin/main)`
   and `base=$(git rev-parse upstream/main)`.
2. Create a review branch at `$old` and run
   `git rebase --rebase-merges "$base"`. For a conflict use `git status`,
   inspect both changes and the fork's intent, and edit/add the resolution
   before `git rebase --continue`. Use `git rebase --abort` if uncertain.
   For workflow changes that Git merged cleanly, inspect
   `git diff "$old" HEAD -- .github/workflows/` and verify the repository and
   branch guards, pinned actions and job token permissions still hold.
3. Run the checks below and review `git diff "$base" HEAD`. Share the
   candidate on a fork review branch for maintainer approval. After approval
   and a fresh upstream check, an authorized maintainer can update fork
   `main` with `git push --force-with-lease="refs/heads/main:$old" origin
   HEAD:refs/heads/main`. If either ref moved, rebase and test again.
   Never push the candidate to upstream.
4. Optional follow-up: schedule a read-only check of failed **Fork nightly
   upstream sync** runs. On an unresolved conflict, notify the maintainer
   with the run and conflicting files, and suggest a review branch for the
   fix. Do not have the watcher write to `main`, alter workflow permissions
   or decide semantic conflicts. This repository does not install that watcher.

### Verify after every rebase

- Confirm `git merge-base --is-ancestor <upstream-sha-from-sync> origin/main`
  and inspect the remaining fork diff. For a local candidate substitute
  `HEAD` for `origin/main`. Run
  `make simple-responder && make test-all && make test-cmd && make test-ui &&
  CGO_ENABLED=0 make native`.
- Check Actions for the image run corresponding to that fork `main` SHA.
  Run `docker pull ghcr.io/jacobtomlinson-bot/llama-swap:latest` and
  `docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}'
  ghcr.io/jacobtomlinson-bot/llama-swap:latest`; expect the current fork
  `main` SHA. Verify it matches the published run's recorded digest.
- Run the real proxy smoke test using the fake model in
  [synthetic-data.md](docs/development/synthetic-data.md). Add
  `aliases: ["fast-alias"]` under `fake-fast`, then send the documented
  streaming chat request with `"model":"fast-alias"`.
  Scrape `curl -fsS localhost:8080/metrics | grep '^llamaswap_model_'`.
  Expect positive TTFT, a positive prefill rate, and a generation rate near
  200 for `model="fake-fast"`, with no `model="fast-alias"` sample.
