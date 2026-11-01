## Why

The release commits the updated `CHANGELOG.md` back to `main` on every release. semantic-release recommends against committing during a release, and the release notes it generates are already published on the GitHub Release by GoReleaser, so `CHANGELOG.md` duplicates them. The `gjed-release` app token stays for the tag and the GitHub Release.

## What Changes

- Remove `@semantic-release/changelog` and `@semantic-release/git` from `.releaserc.js` and `package.json`.
- The release no longer writes or commits `CHANGELOG.md`. Release notes live only on the GitHub Release.
- **BREAKING** for readers of `CHANGELOG.md`: the file is never created. It does not exist on `main` today, so nothing is lost.

## Capabilities

### New Capabilities

### Modified Capabilities

- `ci-release`: the release no longer maintains `CHANGELOG.md` or commits to `main`; release notes are published on the GitHub Release only.

## Impact

- `.releaserc.js`, `package.json`, `pnpm-lock.yaml`
- `.github/workflows/release.yml`: comments only, no behaviour change
- The release job keeps the `gjed-release` app token: semantic-release pushes the version tag with it and GoReleaser creates the GitHub Release with it. Only the commit to `main` goes away.
