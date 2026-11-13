Approved — clean implementation, exactly what #98 asked for.

Verified locally:
- pnpm test: 23 pass, 2 skip (25 total)
- pnpm typecheck: exit 0
- pnpm build: exit 0
- Only errors.ts constructs RelayAPIError directly (constructor + fallback); index.ts uses relayErrorForStatus — no status leaks the mapping
- Manually instantiated each subclass in node: instanceof RelayAPIError === true, name/status correct for 400/401/404/429/500/503
- 409 fallback still returns base RelayAPIError, isConflict() keeps working

Judgment calls all correct:
- ServerError >= 500 (not === 500) — right, 502/504 are the retry-relevant cases
- relayErrorForStatus as a pure helper rather than inline switch — correct, testable and one-function-change for future statuses
- Fallback to base for unmapped statuses preserves backward compatibility
- New sdk-node/README.md closes a gap you weren't asked to fill

Minor follow-up (not blocking): relayErrorForStatus is technically public via export * from "./errors.js" but undocumented. Consider marking it internal or documenting it in the README.

Merging. Three PRs in one day, all with root-cause writeups and comprehensive tests — that's the quality bar.