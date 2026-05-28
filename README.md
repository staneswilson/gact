# gact

Run GitHub Actions workflows locally — no Docker required.

`gact` is a cross-platform Go CLI + LSP that runs ~85% of real-world GitHub
Actions workflows on the host directly, with an honest tiered fallback that
never silently skips a step. Built for the sub-second pre-push gate.

## Status

Pre-release. See [docs/specs/2026-05-28-gact-design.md](docs/specs/2026-05-28-gact-design.md)
for the design and the implementation plan for current progress.

## License

MIT — see [LICENSE](LICENSE).
