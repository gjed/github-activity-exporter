## 1. Release configuration

- [ ] 1.1 Remove `@semantic-release/changelog` and `@semantic-release/git` from `.releaserc.js`
- [ ] 1.2 Remove both packages from `package.json` and refresh `pnpm-lock.yaml`

## 2. Verification

- [ ] 2.1 `pnpm install --frozen-lockfile` succeeds
- [ ] 2.2 `pnpm exec semantic-release --dry-run --no-ci` loads the remaining plugins without error
