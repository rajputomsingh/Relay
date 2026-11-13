Merged after verification — thanks @IG-ABHINAV, this is exactly the kind of root-cause analysis I wanted.

**Verification on my side:**

- Applied the patch locally and reproduced the bug on `main`: `delivery worker configuration invalid: poll interval must be positive` with only `MONGODB_URI`, `MONGODB_DATABASE`, `RELAY_API_KEY` set.
- With this PR applied: worker starts polling, no error, `MONGODB_TEST_URI` set → 102/102 unit tests + all endpoint integration tests pass.
- Confirmed `LeaseDuration: 30s` matches `bench`'s `productionLeaseDuration` and is safely under `orphan_threshold: 5min` in the reconciler config, so the timing chain is consistent.
- Ran `go vet ./...` and `gofmt -l cmd internal` — both clean, matching your report.

**Root cause writeup is excellent** — the framing "parsing was correct, value was never consumed" is the right way to describe this to future readers who will `git blame` in a year.

**On the `RELAY_MAX_DELIVERY_ATTEMPTS` dead config you flagged** — good catch. Filing as a separate issue is the right call. Please do open it, and mention it in the PR description so it does not get lost. If you would rather I file it, say so and I will.

**On the missing-MongoDB verification** — your reasoning holds: `validateConfig`'s rejection of `PollInterval <= 0` plus the new wiring test that produces 2s/5s covers the failure mode. I ran the live reproduction on my side and it passes.

Merging now. If you want to keep contributing, #97 (`.env` follow-up) is available and you already have the config-loading context.

Thanks for the careful work — this is the level of quality I want every Relay PR to have.