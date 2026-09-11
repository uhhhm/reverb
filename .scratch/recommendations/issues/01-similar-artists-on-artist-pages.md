# 01: Similar artists on artist pages

**What to build:** An artist page shows a "Fans also like" section of related artists from Deezer. This is the first path through the recommendation pipeline. It adds an optional similarity capability for search sources, detected by type assertion like `DiscographyProvider`, which Deezer implements. It also adds a recommendation module that owns candidate gathering and returns recommendations through a new API endpoint (edit OpenAPI first, per `docs/contracts.md`).

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] Artist pages for search-source and library artists show up to ~10 related artists, each linking to its artist page
- [ ] The similarity capability is optional: sources without it are skipped, and the section is hidden when no source provides it
- [ ] Results are cached so reopening a page doesn't re-query Deezer
- [ ] Source failures and timeouts hide the section instead of breaking the page
- [ ] Adapter tests use recorded fixtures, the module has unit tests, and `make contracts-check` and `make check` pass
