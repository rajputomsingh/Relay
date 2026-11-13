## Problem

Contributors currently need to configure MONGODB_URI, MONGODB_DATABASE, and RELAY_API_KEY in their shell before running Relay. Missing values lead to avoidable startup errors.

## Scope

Add local .env support:

1. Load .env from the current working directory when the file exists.
2. Do not override variables already set in the process environment.
3. Return a clear error if an existing .env file has invalid syntax.
4. Maintain .env.example with every variable used by internal/config/config.go, including requiredness and the 32-character API-key requirement.
5. Ensure .env is ignored by Git.
6. Document the setup in CONTRIBUTING.md.

## Acceptance Criteria

- [ ] Copying .env.example to .env allows local startup without manually exporting each variable.
- [ ] Explicit process environment variables take precedence over .env.
- [ ] .env is not committed.
- [ ] .env.example lists every configuration variable.
- [ ] CONTRIBUTING.md documents the workflow.
- [ ] A test verifies that the process environment wins over .env.

## Hints

github.com/joho/godotenv is an option. A small standard-library loader is also acceptable. Check the existing .gitignore before changing it.

**Size:** S (~2 hours)
