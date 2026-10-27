---
id: ci-1debd1a5
auditor: ci
severity: medium
category: reliability
area: .github/workflows/test.yml
status: open
found: 2026-10-27
location: .github/workflows/test.yml:14-16
location_sha: ac75165a02107667
---

# Test and CodeQL are not required status checks; only PR Lint gates merges to main

## Problem

Class: non-gating-check (Verified). test.yml runs on pull_request and is the only workflow that runs
`go test`, golangci-lint, govulncheck, the condition-coverage gate, the notices check and the
cross-compile. codeql.yml also runs on pull_request. Neither job is a required status check: the
`main` ruleset requires exactly one context, `PR Lint`.

## Evidence

Source: manual + `gh api`.
`gh api repos/ivuorinen/a/branches/main/protection` → 404 "Branch not protected" (no classic
protection).
`gh api repos/ivuorinen/a/rulesets/7082712` → rules: deletion, non_fast_forward,
required_linear_history, pull_request, required_status_checks: ["PR Lint"], copilot_code_review.
.github/workflows/test.yml:14-16 defines job `test` with `name: Test`; codeql.yml:21-22 defines job
`analyze` with `name: Analyze` (matrix → "Analyze (actions)", "Analyze (go)").

## Impact

A PR whose tests fail, whose lint fails, that pulls in a vulnerable module, that drops a condition
below 100%, that leaves THIRD_PARTY_NOTICES.md stale, or that breaks a release target can be merged
by anyone with merge rights while those checks are red. The notices and cross-compile checks exist
specifically to stop a broken state reaching a tag; not gating them means the first enforcement is
the release job.

## Fix

Add the check names to the ruleset (user runs this; never auto-applied). The names are the check
runs reported on `main` (`gh api repos/ivuorinen/a/commits/main/check-runs`):

```bash
gh api repos/ivuorinen/a/rulesets/7082712 \
  | jq '{name, target, enforcement, conditions, bypass_actors,
         rules: [.rules[] | if .type == "required_status_checks"
           then .parameters.required_status_checks +=
             [{"context": "Test"}, {"context": "Analyze (go)"}, {"context": "Analyze (actions)"}]
           else . end]}' \
  | gh api -X PUT repos/ivuorinen/a/rulesets/7082712 --input -
```

Verify with: `gh api repos/ivuorinen/a/rulesets/7082712 --jq '.rules[] |
select(.type=="required_status_checks") | .parameters.required_status_checks[].context'`. Test runs
on merge_group already, so adding it works with a merge queue too.

Open (2026-10-27 run): ruleset changes are never applied by the audit; waiting on the maintainer to
run the command above.
