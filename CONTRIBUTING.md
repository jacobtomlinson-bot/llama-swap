# Contributing to this fork

Humans and authorized agents can both commit and push directly to `main` in
`jacobtomlinson-bot/llama-swap`. No pull request is needed.

1. Keep changes focused. Review your diff and pass the applicable checks in
   [AGENTS.md](AGENTS.md) before pushing.
2. Fetch the fork's `main` and keep your commits linear on top of it. If it
   moved, rebase and rerun the applicable checks. Push with a fast-forward only
   for ordinary changes; don't create merge commits on fork `main`.
3. Verify `git remote get-url --push origin` points to
   `jacobtomlinson-bot/llama-swap`, then push the checked commit with
   `git push origin HEAD:refs/heads/main`.

Use `mostlygeek/llama-swap` as a read-only upstream. This fork workflow never
pushes to upstream or opens issues or pull requests there. Only a tested
upstream sync may rewrite the fork's history using the exact ref lease and
authorization described in [FORK_FEATURES.md](FORK_FEATURES.md).
