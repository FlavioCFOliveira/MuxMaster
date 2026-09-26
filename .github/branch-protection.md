# Branch Protection

This file specifies the protection rule for the `main` branch. Project
policy requires it; apply it via the GitHub UI or the `gh api` command
below, and check it with the verification command at the end of this
file.

## Current status

The rule is **required by project policy but not currently applied** on
GitHub. On 2026-09-26, `gh api repos/FlavioCFOliveira/MuxMaster/branches/main/protection`
returned `Branch not protected` (HTTP 404), and the repository has no
rulesets. Until the rule is applied, GitHub does not block force pushes
to `main` or its deletion.

## How changes reach `main`

The repository follows gitflow without pull requests. The maintainer
merges `release/*` and `hotfix/*` branches into `main` with `--no-ff`
merge commits, tags the release, and pushes directly. The rule below is
designed for that workflow:

- **It must allow merge commits.** "Require linear history" rejects any
  push that contains a merge commit, so it would block every gitflow
  release and hotfix merge. It is therefore disabled.
- **It cannot require pull requests.** With "Require a pull request
  before merging" enabled, changes reach the branch only through an
  approved pull request, which blocks the direct pushes this workflow
  depends on. It is therefore disabled.
- **It cannot gate a direct push on CI.** GitHub requires every
  required status check to be successful, skipped or neutral *before*
  a commit can be pushed to the protected branch. A `--no-ff` merge
  commit is created locally, so no workflow has run on it when it is
  pushed, and a rule with required status checks rejects the push.
  Required status checks are therefore not configured.

What the rule enforces is limited to preventing history rewrites and
branch deletion on `main`, for administrators too. CI verifies a push
after it lands: `ci.yml` and `commitlint.yml` run on every push to
`main`, `develop`, `release/**` and `hotfix/**`, so each gitflow branch
is checked before it is merged onward, and the release workflow
(`release.yml`) runs the race-enabled test suite on the tagged commit
before it publishes a GitHub Release. The maintainer must see a green
CI run on the `release/*` or `hotfix/*` branch before merging it into
`main`; GitHub cannot enforce this for a direct push.

Reference: GitHub Docs, [About protected branches](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches)
("Require status checks before merging", "Require linear history",
"Require pull request reviews before merging").

## Required settings

- **Do not allow bypassing the above settings** — repository
  administrators follow the same rules.
- **Block force pushes** on `main` (the default for a protected branch).
- **Block branch deletion** on `main` (the default for a protected
  branch).

Settings that must stay **disabled**, for the reasons above: "Require a
pull request before merging", "Require status checks to pass before
merging", "Require linear history".

## CI checks expected green before a merge into `main`

These jobs run on every push to a `release/*` or `hotfix/*` branch. They
are not required status checks (see above); the maintainer checks them
before merging.

| Workflow file        | Job name                                          |
|----------------------|---------------------------------------------------|
| `ci.yml`             | `Test (Go 1.27.1 on ubuntu-latest)`               |
| `ci.yml`             | `Test (Go 1.27.1 on macos-latest)`                |
| `ci.yml`             | `Test (Go 1.27.1 on windows-latest)`              |
| `ci.yml`             | `Test (Go stable on ubuntu-latest)`               |
| `ci.yml`             | `Test (Go stable on macos-latest)`                |
| `ci.yml`             | `Test (Go stable on windows-latest)`              |
| `ci.yml`             | `Test (linux/arm64)`                              |
| `ci.yml`             | `Harness tests (security auditors)`               |
| `ci.yml`             | `Lint (golangci-lint)`                            |
| `ci.yml`             | `SAST (staticcheck)`                              |
| `ci.yml`             | `SAST (gosec)`                                    |
| `ci.yml`             | `SAST (semgrep custom rules)`                     |
| `ci.yml`             | `Vulnerability scan (govulncheck)`                |
| `ci.yml`             | `CHANGELOG.md updated`                            |
| `ci.yml`             | `api.md is up to date`                            |
| `ci.yml`             | `Coverage`                                        |
| `commitlint.yml`     | `Validate commit messages`                        |

`ci.yml` `Go tip (canary)` is advisory (`continue-on-error`).
`ci.yml` `API breaking-change check (apidiff)` runs on every push as an
advisory check (it reports incompatible changes but never fails) and
blocks on pull requests. `bench.yml` and `codeql.yml` run on pull
requests against `main`; CodeQL also runs on pushes to `main` and weekly.

## Apply via the GitHub UI

1. Open **Settings → Branches** in the repository and choose **Add
   classic branch protection rule**.
2. Set **Branch name pattern** to `main`.
3. Leave **Require a pull request before merging**, **Require status
   checks to pass before merging** and **Require linear history**
   disabled.
4. Enable **Do not allow bypassing the above settings**.
5. Leave **Allow force pushes** and **Allow deletions** disabled, then
   choose **Create**.

## Apply via gh CLI

The same rule can be applied through the GitHub REST API
([Update branch protection](https://docs.github.com/rest/branches/branch-protection#update-branch-protection)).
The request body is JSON, so it is passed on standard input. The API
requires the `required_status_checks`, `enforce_admins`,
`required_pull_request_reviews` and `restrictions` keys; `null` disables
the first, third and fourth:

```sh
REPO=FlavioCFOliveira/MuxMaster
BRANCH=main

gh api -X PUT "repos/$REPO/branches/$BRANCH/protection" --input - <<'JSON'
{
  "required_status_checks": null,
  "enforce_admins": true,
  "required_pull_request_reviews": null,
  "restrictions": null,
  "required_linear_history": false,
  "allow_force_pushes": false,
  "allow_deletions": false
}
JSON
```

The call requires administrator access to the repository.

## Verifying the configuration

```sh
gh api "repos/$REPO/branches/$BRANCH/protection" \
  | jq '{admins: .enforce_admins.enabled,
         linear: .required_linear_history.enabled,
         force_pushes: .allow_force_pushes.enabled,
         deletions: .allow_deletions.enabled,
         status_checks: .required_status_checks,
         reviews: .required_pull_request_reviews}'
```

The expected output is `admins: true`, `linear: false`,
`force_pushes: false`, `deletions: false`, and `null` for
`status_checks` and `reviews`. A `Branch not protected` (HTTP 404)
response means the rule has not been applied.

## Maintenance

When a workflow job is added or renamed, update the table above. The
`PUT` request is idempotent: re-running it replaces the whole rule.
