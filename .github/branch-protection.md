# Branch Protection

The `main` branch is protected. Settings below are applied via the
GitHub UI (Settings → Branches → Branch protection rules) **or** via
`gh api` commands committed at the bottom of this file.

## Required settings

- **Restrict pushes that create matching branches** — direct pushes to
  `main` are blocked; merges only via Pull Request.
- **Require a pull request before merging** — at least 1 reviewer
  approval required; stale reviews dismissed when new commits push.
- **Require status checks to pass before merging** — every check
  enumerated below must report success on the PR's HEAD commit.
- **Require conversation resolution before merging** — review threads
  must be marked resolved.
- **Require linear history** — squash or rebase merge only; no merge
  commits.
- **Do not allow bypassing the above settings** — repository admins
  follow the same rules.

## Required status checks

Each check corresponds to a job in `.github/workflows/`:

| Workflow file        | Job name                                          |
|----------------------|---------------------------------------------------|
| `ci.yml`             | `Test (Go 1.26 on ubuntu-latest)`                 |
| `ci.yml`             | `Test (Go 1.26 on macos-latest)`                  |
| `ci.yml`             | `Test (Go 1.26 on windows-latest)`                |
| `ci.yml`             | `Test (Go stable on ubuntu-latest)`               |
| `ci.yml`             | `Test (linux/arm64)`                              |
| `ci.yml`             | `Lint (golangci-lint)`                            |
| `ci.yml`             | `SAST (staticcheck)`                              |
| `ci.yml`             | `SAST (gosec)`                                    |
| `ci.yml`             | `Vulnerability scan (govulncheck)`                |
| `ci.yml`             | `Coverage`                                        |
| `ci.yml`             | `API breaking-change check (apidiff)`             |
| `ci.yml`             | `api.md is up to date`                            |
| `codeql.yml`         | `Analyze (Go)`                                    |
| `bench.yml`          | `Compare benchmarks against main`                 |

## Apply via gh CLI

The same rules can be applied through the GitHub REST API. This is
the canonical recipe — copy-paste into a shell session:

```sh
REPO=FlavioCFOliveira/MuxMaster
BRANCH=main

gh api -X PUT "repos/$REPO/branches/$BRANCH/protection" \
  -F required_status_checks.strict=true \
  -F required_status_checks.contexts[]="Test (Go 1.26 on ubuntu-latest)" \
  -F required_status_checks.contexts[]="Test (Go 1.26 on macos-latest)" \
  -F required_status_checks.contexts[]="Test (Go 1.26 on windows-latest)" \
  -F required_status_checks.contexts[]="Test (Go stable on ubuntu-latest)" \
  -F required_status_checks.contexts[]="Test (linux/arm64)" \
  -F required_status_checks.contexts[]="Lint (golangci-lint)" \
  -F required_status_checks.contexts[]="SAST (staticcheck)" \
  -F required_status_checks.contexts[]="SAST (gosec)" \
  -F required_status_checks.contexts[]="Vulnerability scan (govulncheck)" \
  -F required_status_checks.contexts[]="Coverage" \
  -F required_status_checks.contexts[]="API breaking-change check (apidiff)" \
  -F required_status_checks.contexts[]="api.md is up to date" \
  -F required_status_checks.contexts[]="Analyze (Go)" \
  -F enforce_admins=true \
  -F required_pull_request_reviews.required_approving_review_count=1 \
  -F required_pull_request_reviews.dismiss_stale_reviews=true \
  -F required_pull_request_reviews.require_code_owner_reviews=true \
  -F required_conversation_resolution=true \
  -F required_linear_history=true \
  -F restrictions=null \
  -F allow_force_pushes=false \
  -F allow_deletions=false
```

## Verifying the configuration

```sh
gh api repos/$REPO/branches/$BRANCH/protection | jq '.required_status_checks.contexts'
```

The output should match the `Required status checks` table above.

## Maintenance

When a new required workflow is added (or an existing job renamed),
update both this file *and* the branch-protection rule via the gh
command above. The `gh` invocation is idempotent — re-running it
replaces the contexts list atomically.
