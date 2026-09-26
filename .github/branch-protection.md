# Branch Protection

This file specifies the protection rule for the `main` branch. Project
policy requires it; apply it via the GitHub UI or the `gh api` command
below, and check it with the verification command at the end of this
file.

## Current status

The rule is **required by project policy but not currently applied** on
GitHub. On 2026-09-26, `gh api repos/FlavioCFOliveira/MuxMaster/branches/main/protection`
returned `Branch not protected` (HTTP 404), and the repository has no
rulesets. Until the rule is applied, GitHub does not block direct pushes
to `main`, and the status checks below are not enforced before a merge.

## Required settings

- **Require a pull request before merging** — at least 1 approving
  review, including one from a code owner (`.github/CODEOWNERS`); stale
  approvals are dismissed when new commits are pushed. Once applied,
  this blocks direct pushes to `main`.
- **Require status checks to pass before merging** — every check
  enumerated below must report success, and the branch must be up to
  date with `main`.
- **Require conversation resolution before merging** — review threads
  must be marked resolved.
- **Require linear history** — squash or rebase merge only; no merge
  commits.
- **Do not allow bypassing the above settings** — repository
  administrators follow the same rules.
- **Block force pushes and branch deletion** on `main`.

## Required status checks

Each check corresponds to a job in `.github/workflows/`:

| Workflow file        | Job name                                          |
|----------------------|---------------------------------------------------|
| `ci.yml`             | `Test (Go 1.27.1 on ubuntu-latest)`                 |
| `ci.yml`             | `Test (Go 1.27.1 on macos-latest)`                  |
| `ci.yml`             | `Test (Go 1.27.1 on windows-latest)`                |
| `ci.yml`             | `Test (Go stable on ubuntu-latest)`               |
| `ci.yml`             | `Test (Go stable on macos-latest)`                |
| `ci.yml`             | `Test (Go stable on windows-latest)`              |
| `ci.yml`             | `Test (linux/arm64)`                              |
| `ci.yml`             | `Lint (golangci-lint)`                            |
| `ci.yml`             | `SAST (staticcheck)`                              |
| `ci.yml`             | `SAST (gosec)`                                    |
| `ci.yml`             | `Vulnerability scan (govulncheck)`                |
| `ci.yml`             | `Coverage`                                        |
| `ci.yml`             | `API breaking-change check (apidiff)`             |
| `ci.yml`             | `api.md is up to date`                            |
| `codeql.yml`         | `Analyze (Go)`                                    |

`bench.yml` (`Compare benchmarks against main`) is deliberately not a
required check: it runs only when a pull request changes `.go` files,
`go.mod`, `go.sum` or the workflow itself, and a required check whose
workflow is skipped by a path filter stays pending and blocks the merge.
It still fails the pull request it runs on when a benchmark regresses by
more than 10%.

## Apply via the GitHub UI

1. Open **Settings → Branches** in the repository and choose **Add
   classic branch protection rule**.
2. Set **Branch name pattern** to `main`.
3. Enable **Require a pull request before merging**, set **Required
   approvals** to 1, and enable **Dismiss stale pull request approvals
   when new commits are pushed** and **Require review from Code Owners**.
4. Enable **Require status checks to pass before merging** and **Require
   branches to be up to date before merging**, then add every check in
   the table above. GitHub lists a check only after it has run at least
   once on the repository.
5. Enable **Require conversation resolution before merging** and
   **Require linear history**.
6. Enable **Do not allow bypassing the above settings**.
7. Leave **Allow force pushes** and **Allow deletions** disabled, then
   choose **Create**.

## Apply via gh CLI

The same rule can be applied through the GitHub REST API
([Update branch protection](https://docs.github.com/rest/branches/branch-protection#update-branch-protection)).
The request body is JSON, so it is passed on standard input:

```sh
REPO=FlavioCFOliveira/MuxMaster
BRANCH=main

gh api -X PUT "repos/$REPO/branches/$BRANCH/protection" --input - <<'JSON'
{
  "required_status_checks": {
    "strict": true,
    "contexts": [
      "Test (Go 1.27.1 on ubuntu-latest)",
      "Test (Go 1.27.1 on macos-latest)",
      "Test (Go 1.27.1 on windows-latest)",
      "Test (Go stable on ubuntu-latest)",
      "Test (Go stable on macos-latest)",
      "Test (Go stable on windows-latest)",
      "Test (linux/arm64)",
      "Lint (golangci-lint)",
      "SAST (staticcheck)",
      "SAST (gosec)",
      "Vulnerability scan (govulncheck)",
      "Coverage",
      "API breaking-change check (apidiff)",
      "api.md is up to date",
      "Analyze (Go)"
    ]
  },
  "enforce_admins": true,
  "required_pull_request_reviews": {
    "required_approving_review_count": 1,
    "dismiss_stale_reviews": true,
    "require_code_owner_reviews": true
  },
  "required_conversation_resolution": true,
  "required_linear_history": true,
  "restrictions": null,
  "allow_force_pushes": false,
  "allow_deletions": false
}
JSON
```

The call requires administrator access to the repository.

## Verifying the configuration

```sh
gh api "repos/$REPO/branches/$BRANCH/protection" | jq '.required_status_checks.contexts'
```

The output should match the `Required status checks` table above. A
`Branch not protected` (HTTP 404) response means the rule has not been
applied.

## Maintenance

When a new required workflow is added (or an existing job renamed),
update both this file *and* the branch-protection rule via the `gh`
command above. The `PUT` request is idempotent: re-running it replaces
the whole rule, including the contexts list.
