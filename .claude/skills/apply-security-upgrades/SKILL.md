---
name: apply-security-upgrades
description: Clear the whole dependency and vulnerability backlog on one branch and get govulncheck, Snyk, SonarCloud, lint and the 100% coverage gate green. Use periodically, or when Dependabot PRs are piling up red.
---

# Apply security upgrades

Collapse every outstanding security item into a single reviewable branch, then
fix whatever the gates report. One branch, one PR, one CI run instead of five
that each burn a full matrix.

## Why a single branch

Dependabot opens one PR per ecosystem group. Each one runs the whole CI matrix,
each one has to be merged and then rebased against the others, and none of them
can fix a problem that lives outside its own diff, such as a stale `go` directive
or a missing egress endpoint. Merging them together first means one CI run, one
review, and a place to put the fixes that no individual bump could carry.

## Procedure

### 1. Survey

Start with the Dependabot PRs, if there are any:

```bash
gh pr list --limit 50 --json number,title,headRefName,author \
  --jq '.[] | select(.author.login=="app/dependabot") | "\(.number)\t\(.headRefName)\t\(.title)"'
```

An empty list is not the same as "nothing to do". Confirm the machinery is
actually switched on before believing it:

```bash
gh api repos/Tight-Line/gatekeeper --jq '.security_and_analysis'
gh api repos/Tight-Line/gatekeeper/vulnerability-alerts -i | head -1   # 204 = alerts on
gh api repos/Tight-Line/gatekeeper/dependabot/alerts --jq 'length'
test -f .github/dependabot.yml && echo "version updates configured" || echo "NO dependabot.yml"
```

`dependabot_security_updates: disabled` plus no `dependabot.yml` means no PRs
will ever be opened, so a quiet backlog looks identical to a clean repo. Zero
alerts from a repo with no dependency graph means the same thing. In that state
the survey has to come from the tools directly; see step 4.

Check that the scanners are still switched on, too. GitHub disables a workflow
whose only trigger is `schedule` after 60 days without repository activity, and
it does so silently: the workflow simply stops running, no check goes red, and
the last run sits there green forever.

```bash
gh api repos/Tight-Line/gatekeeper/actions/workflows \
  --jq '.workflows[] | "\(.state)\t\(.name)"'   # look for disabled_inactivity
gh workflow enable snyk.yml                     # to switch one back on
```

This is how `snyk.yml` went dark here: it last ran on its weekly cron in
August, was disabled for inactivity, and then did not run on the 2026-09 pass
at all until it was re-enabled by hand.

For each PR that does exist, check it is a forward bump and not stale; `main` may
already carry a newer bump of the same group:

```bash
git fetch origin
git diff origin/main...origin/<branch>
```

Capture the current gate status, and **capture it for `main` too**:

```bash
gh pr checks <n>
gh run list --branch main --limit 15 \
  --json conclusion,name,headSha,createdAt \
  --jq '.[] | "\(.conclusion)\t\(.name)\t\(.headSha[0:8])"'
```

> If a check is red on *every* PR including ones that touch only the Dockerfile,
> it is not the dependency bumps. It is infrastructure, and it is almost
> certainly also red on `main`. Diagnose it before touching any dependency.

Check the run dates, not just the conclusions. Runs whose `headSha` has not moved
in months mean nothing has been scanned in months, however green they look.

### 2. Build the branch

```bash
git checkout -b chore/security-upgrades-<YYYY-MM> origin/main
```

**Cherry-pick; do not merge.** `main` has a strictly linear history. A branch
built with `git merge` fails a later rebase with `This branch cannot be rebased
due to conflicts` even when GitHub simultaneously reports the PR as `MERGEABLE`
and `CLEAN`: the conflict resolution lives *inside* a merge commit, rebase
discards it, and the conflict comes back with nothing to resolve it.
Cherry-picking resolves the conflict inline, in a real commit, where rebase can
carry it.

Apply in a deliberate order; cheapest and least conflict-prone first, Go modules
last, and within Go modules the **grouped minor/patch bump before the security
bump** so the security pin is the one that survives:

1. `github-actions` group
2. `docker` bumps
3. `gomod` minor/patch group
4. `gomod` security group

```bash
# each Dependabot branch is a single commit; -x records where it came from
git cherry-pick -x origin/<branch>
```

Cherry-pick preserves `dependabot[bot]` as the author, so the audit trail
survives. Merging this PR still auto-closes the Dependabot PRs, because the PR
body's `Closes #NNN` keywords do that regardless of whether their exact SHAs land.

### 3. Resolve go.mod / go.sum conflicts

Two Go module changes will conflict when the security bump goes on top of the
grouped one. Do **not** hand-merge the version lines. Take the already-applied
side, then re-apply the incoming pin through the toolchain so `go.sum` stays
internally consistent:

```bash
git checkout --ours go.mod go.sum
go get <module>@<version>   # re-apply each security pin the incoming commit carried
go mod tidy                 # slow; run it in the background
git add go.mod go.sum
git cherry-pick --continue  # or `git rebase --continue`
```

`--ours` means "what is already applied" during both cherry-pick and rebase,
which is the grouped bump. That is the side to keep. Note this is the opposite
sense from `git merge`, where `--ours` is the branch you are merging into.

Rule for each conflicting line: **take the higher version**. The minor/patch
group is often newer for transitive deps such as `golang.org/x/sys` and
`protobuf`, while the security group is newer for the one module it targets.
Both need to win where they are ahead.

`go mod tidy` may legitimately prune `go.sum` lines for the version being
replaced. That is correct, not drift. But it means the tree is no longer
byte-identical to a previously verified one, so re-run the gate rather than
trusting the earlier green run.

### 4. Find what is actually outstanding

With no Dependabot PRs to inherit from, the backlog comes from the tools:

```bash
go tool govulncheck ./...                                      # symbol-level, authoritative
go list -u -m -f '{{if and .Update (not .Indirect)}}{{.Path}} {{.Version}} -> {{.Update.Version}}{{end}}' all
```

`govulncheck` is the one that decides what counts as a security fix. Everything
else `go list -u` turns up is a routine bump and belongs under `### Changed`.

Resolve each finding to its real fixed version rather than trusting the single
version `govulncheck` prints, which is only the fix for the line you are on:

```bash
curl -s https://vuln.go.dev/ID/GO-2026-XXXX.json \
  | ruby -rjson -e 'd=JSON.parse(STDIN.read); puts d["summary"]
      d["affected"].each{|a| puts "  #{a["package"]["name"]}: " +
        a["ranges"].map{|r| r["events"].map{|e| e.to_a.flatten.join(":")}.join(" ")}.join(" | ")}'
```

That prints every affected minor line and where each one was patched, which is
what decides the `go` directive target in the next step.

### 5. Fix the gates

Run the repo gate locally before pushing:

```bash
make check                 # lint + test-coverage-check + build-all
go tool govulncheck ./...
```

Both are slow. Run them in the background and **read the real output**, not a
wrapper's exit status. `govulncheck` exits `3` on findings, and a trailing
`echo EXIT=$?` through a pipe reports the pipe's status rather than the tool's.

Known classes of problem, in the order they tend to bite:

#### The stale `go` directive (Dependabot cannot fix this)

`govulncheck` reports Go **standard library** vulnerabilities against the Go
version the module builds with. Dependabot bumps modules and the Dockerfile
builder image; it never bumps the `go` directive in `go.mod`. So the stdlib
quietly rots and `govulncheck` fails with findings that no dependency PR can
clear.

```bash
# what patch releases exist
curl -s "https://go.dev/dl/?mode=json&include=all" \
  | grep -oE '"version": "go1\.[0-9]+\.[0-9]+"' | sort -u -V | tail -20
# setup-go must be able to install it
curl -s "https://raw.githubusercontent.com/actions/go-versions/main/versions-manifest.json" \
  | grep -oE '"version": "1\.[0-9]+\.[0-9]+"' | sort -u -V | tail -12
```

Prefer the **latest patch on the current minor line**: a minor bump changes
language semantics and vet/lint behavior and does not belong in a security pass.
The exception is when the current line is end-of-life. Go supports only the two
most recent minors, so once `1.N+2` ships, `1.N` stops getting patches and
staying on it means the next stdlib CVE has no fix at all. In that case move to
the oldest still-supported line, not to the newest one, so the semantic drift is
as small as it can be while still leaving somewhere to bump to next time. The
2026-09 pass moved `1.25.6` to `1.26.8` for exactly this reason: every finding
was patched in `1.25.13`, but `1.27` had shipped and `1.25.14` was the end of
that line.

```bash
go mod edit -go=<version>    # does not add a `toolchain` directive; keep it that way
go tool govulncheck ./...    # must print "No vulnerabilities found."
```

The version is pinned in more places than `go.mod`. Miss one and CI keeps
building against the vulnerable toolchain while the local run looks clean:

```bash
grep -rn "go-version\|golang:" .github/workflows/ Dockerfile Dockerfile.relay
```

Prefer `go-version-file: go.mod` in every workflow over a hardcoded string, so
there is exactly one place to bump next time. The Dockerfiles track a minor line
(`golang:1.26-alpine`) and pick up patches on their own, so they only need
touching on a minor bump.

Confirm the bump against the real output. A local toolchain older than the new
directive is downloaded automatically, so a passing run also proves the toolchain
resolved. Beware the reverse: a local toolchain *newer* than the directive is
used as-is, so `govulncheck` locally reports against that newer toolchain, not
against the directive. Check which one it used before concluding anything:

```bash
go tool govulncheck ./... | grep -oE 'go1\.[0-9]+\.[0-9]+' | sort -u
```

The Dockerfile builder image and the `go` directive do **not** need to match. A
newer toolchain building an older-directive module is fine and normal.

#### Snyk or Sonar red for reasons that are not findings

Check *why* the job is red before hunting for vulnerabilities. A scanner that
dies in setup reports the same red X as a scanner that found a critical CVE.

```bash
RUN=$(gh run list --workflow=snyk.yml --branch main --limit 1 --json databaseId --jq '.[0].databaseId')
gh run view "$RUN" --log > /tmp/snyk.log
grep -nE "##\[error\]|Acquiring|ECONNREFUSED|severity|issues found" /tmp/snyk.log
```

`gh run view --log-failed` is often useless here, and so is `tail`: harden-runner
dumps its entire agent log into the job's teardown, so both show eBPF and systemd
chatter instead of the error. Find the failing step by name, then grep only that
step's lines:

```bash
gh run view <run_id> --json jobs \
  --jq '.jobs[] | {name, conclusion, failed: [.steps[]|select(.conclusion=="failure")|.name]}'
JOB=<databaseId from above>
gh run view --job "$JOB" --log | grep -F "<failing step name>" | cut -c1-200 | tail -40
```

Then widen the `cut` on the one interesting line, because the real cause is often
past column 200. A truncated `unable to download ...` can end in `got status
"504 Gateway Timeout"`, which is a transient, not a bug.

#### An expired scanner token looks exactly like a finding

Both scanner tokens expire, and neither failure says so plainly. Snyk is the
honest one; SonarCloud blames its own config:

```
ERROR   Authentication error (SNYK-0005)
Status: 401 Unauthorized

ERROR Failed to query JRE metadata: . Please check the property sonar.token
      or the environment variable SONAR_TOKEN.
```

The Sonar message reads like a workflow bug, and the empty detail after the
colon is the giveaway that the request failed rather than returned anything.
Check the secret's age before touching the workflow:

```bash
gh secret list   # updated dates, which is the closest thing to an expiry
```

Both were dead in the 2026-09 pass, `SONAR_TOKEN` for roughly eight months. Only
a human can rotate them, so raise it early rather than at the end: the branch
cannot go fully green without it, and no amount of allowlist work will help.

#### harden-runner egress blocks (the recurring one)

Every job runs `step-security/harden-runner` with `egress-policy: block` and an
explicit `allowed-endpoints` list. A missing endpoint fails the job with a bare
`connect ECONNREFUSED <ip>:443` and nothing else.

These failures are **latent**. The blocked download usually only happens on a
cache miss or a version change, so an allowlist can be wrong for weeks and pass
anyway, then break everything at once:

- `release-assets.githubusercontent.com:443` is where `actions/setup-go` fetches
  the Go toolchain. It is only downloaded when the runner image does not already
  ship the version in `go.mod`, **so bumping the `go` directive can break every
  job whose allowlist is missing this**. Bump the directive and the endpoint in
  the same change.
- `binaries.sonarsource.com:443` is the Sonar Scanner CLI zip, cached under a
  version-keyed Actions cache, so it only downloads when that cache is cold.
- `vuln.go.dev:443` is govulncheck's database.
- `api.snyk.io:443` is Snyk.
- `cli.codecov.io:443` and `ingest.codecov.io:443` are the Codecov uploader,
  which also reports to `*.ingest.us.sentry.io:443`. It additionally fetches
  its signing key from `keybase.io:443`; block that and the step aborts with
  `Could not verify signature`, which names no host at all.
- `golangci-lint.run:443` serves the JSON schema that `golangci-lint config
  verify` validates `.golangci.yml` against. The action runs that before it
  lints anything, so the job dies in about 13 seconds with a schema load error
  that reads like a config problem.
- `ghcr.io:443`, `pkg-containers.githubusercontent.com:443`,
  `registry-1.docker.io:443`, `auth.docker.io:443` and
  `production.cloudfront.docker.com:443` are the image build and push jobs.

When a job fails at `Set up Go`, compare its allowlist against a job in the same
repo that passes. The passing job's list is the answer.

For a denial that is not an obvious toolchain fetch, get the real egress data
from the StepSecurity run page rather than the Actions log, which does not carry
it: `https://app.stepsecurity.io/github/Tight-Line/gatekeeper/actions/runs/<run_id>`.
The run page lists every destination the job actually reached and which ones were
blocked.

Wildcard rule when adding an entry: wildcard only narrow vendor-owned domains
such as `*.ingest.us.sentry.io`; pin shared multi-tenant infrastructure such as
S3 and GCS buckets to the exact host. Matching is exact-FQDN, so `github.com`
does not cover its subdomains.

Audit every job at once rather than fixing them one red check at a time. System
`python3` here has no `yaml` module; `ruby -ryaml` does:

```bash
ruby -ryaml -e '
Dir[".github/workflows/*.yml"].sort.each do |f|
  y = YAML.load_file(f)
  (y["jobs"]||{}).each do |job, cfg|
    steps = cfg["steps"]||[]
    next unless steps.any?{|s| s["uses"].to_s.include?("actions/setup-go")}
    hr = steps.find{|s| s["uses"].to_s.include?("harden-runner")}
    eps = hr ? hr["with"]["allowed-endpoints"].to_s.split : []
    ok = eps.include?("release-assets.githubusercontent.com:443")
    puts "%-34s %s" % [File.basename(f)+"/"+job, ok ? "ok" : "*** MISSING ***"]
  end
end'
```

Jobs that never invoke `setup-go` but still run `go build`, such as the image
builds, pull a bumped toolchain through the module proxy instead, so they need
`proxy.golang.org:443` rather than `release-assets`. Swap the `setup-go` test
above for that endpoint to check them.

Adding an endpoint to an allowlist may be refused by the permission classifier as
a "security weaken" when done through a scripted `sed` or `python` edit. Make the
edit with the normal file-edit tool instead, so the change is explicit and
reviewable.

#### Unpinned actions

`uses: some/action@v4` resolves to a moving tag, so a compromised or retagged
release runs with the job's token. Pin every `uses:` to a full commit SHA with
the version in a trailing comment, which is also what the `github-actions`
Dependabot ecosystem updates:

```bash
grep -rn "uses:" .github/workflows/ | grep -vE "@[0-9a-f]{40}"
gh api repos/<owner>/<repo>/commits/<tag> --jq '.sha'   # resolve a tag to its SHA
```

`@master` is the worst case and still needs a SHA; keep `# master` as the
trailing comment so Dependabot can still track it.

#### The 100% coverage gate

This repo enforces 100% coverage via `scripts/check-coverage.sh`, wired into
`make test-coverage-check` and into CI. Dependency bumps rarely move it, but a
`go mod tidy` that drops or adds a file can, and a new `tool` directive pulls in
packages that must not end up in the coverage set. Fix a real gap with a test,
not with `coverage:ignore`; see the Test Coverage Exclusions section in
`AGENTS.md` for the narrow cases where an ignore is legitimate, and note that it
asks you to get the developer's agreement first.

The exception is the timing-dependent lines in `internal/relay/redis_manager.go`,
where whether a branch is reached depends on a race between an `XReadGroup` block
timeout and a context deadline. Those come out covered on a fast machine and
uncovered on a CI runner, so this is the one coverage failure that does **not**
reproduce locally. Confirm which kind you have before changing any code:

```bash
for i in 1 2 3 4 5; do
  go test -race -coverprofile=/tmp/c$i.out -covermode=atomic -tags=ci ./internal/relay/ >/dev/null 2>&1
  echo "run $i: $(grep 'redis_manager.go:<line>' /tmp/c$i.out | awk '{print $NF}')"
done
gh run rerun <run_id> --failed   # a pass on re-run settles it
```

Note that the script only looks for `coverage:ignore` on the uncovered line
itself or the line directly above it, so an ignore on an enclosing `if` does not
cover a branch nested inside it.

#### Sonar quality gate

Sonar only fails on **new code**, so dependency bumps rarely move it. If it
fails, read the gate on the PR decoration rather than guessing.

Note that on Dependabot and fork runs `SONAR_TOKEN` and `SNYK_TOKEN` are empty,
so both jobs skip and report green. A green Sonar or Snyk check on a Dependabot
PR means nothing was scanned. They run for real on this branch, because it is
pushed from the repo rather than by Dependabot. `govulncheck` is tokenless and is
the only scanner that gates a Dependabot PR for real, which is the reason it
belongs in `ci.yml` rather than in a workflow of its own.

### 6. Changelog and commit

**A security pass must leave a `### Security` entry under `## [Unreleased]`.**
This is the one thing that cannot be skipped. `scripts/make-tag` refuses to cut a
release when `[Unreleased]` is empty, so with no entry there is no way to ship the
patched build at all, and the fix ends up riding along with whatever feature lands
next. The entry is also the only way a user learns why the patch release is worth
taking, since by definition they cannot see the change.

This is an explicit carve-out from the "do NOT add entries for internal
refactoring" rule in the Changelog Maintenance section of `AGENTS.md`. Write the
advisory IDs and what they affect, not just "bumped dependencies". Routine bumps
carrying no security fix do not each need a line; summarize those under
`### Changed`.

Do not touch version numbers or tags. Releases go through `scripts/make-tag`.

Stage every file you touched. `make lint` and `make test-coverage-check` pass on
working-tree state, but CI only sees what is committed:

```bash
git status
make check     # must be green on the staged tree
```

### 7. Push and verify

```bash
git push -u origin chore/security-upgrades-<YYYY-MM>
gh pr create --fill
```

Then **watch the checks actually go green**. This branch exists specifically to
fix red gates, so a green local run is not the deliverable:

```bash
gh pr checks --watch
```

Merging this PR auto-closes the Dependabot PRs whose commits it contains.

Known transients, neither of which is a real failure:

- Release-asset downloads from GitHub intermittently **504**. They take out one
  job while a sibling pulling the same tarball succeeds.
- `go mod download` intermittently dies on a **`stream error: ... INTERNAL_ERROR`**
  from `proxy.golang.org` mid-zip.

Both clear on a re-run, which only works once the whole run has finished; while
sibling jobs are still going it refuses with "This workflow is already running":

```bash
gh run rerun <run_id> --failed
```

A dependency-heavy failure is worth one check before writing it off, especially
right after resolving a `go.sum` conflict. These two commands settle it locally
in seconds, and "all modules verified" means the checksums are sound and the
failure was the network:

```bash
go mod download && echo ok
go mod verify
```

Verify a scanner actually scanned before calling it green. Both of these skip
silently when their token is absent, and a skipped job reports pass:

```bash
gh run view --job <job_id> --log | grep -F "Run Snyk" | grep -iE "Tested .* dependencies|issues found"
gh run view --job <job_id> --log | grep -F "SonarCloud Scan" | grep -iE "ANALYSIS SUCCESSFUL|EXECUTION"
```

A real Snyk run prints `Tested N dependencies for known issues`; a real Sonar run
prints `ANALYSIS SUCCESSFUL` and `EXECUTION SUCCESS`.

## Checklist

- [ ] Dependabot confirmed switched on, or `dependabot.yml` added, so the backlog is visible next time
- [ ] Every open Dependabot PR either cherry-picked onto the branch or explicitly noted as superseded
- [ ] `git rev-list --merges origin/main..HEAD` is empty, so "Rebase and merge" works
- [ ] `go.mod` and `go.sum` conflicts resolved to the higher version on both sides
- [ ] `go` directive on a supported minor line at its latest patch; `govulncheck` clean
- [ ] Every Go version pin agrees: `go.mod`, all workflows, both Dockerfiles
- [ ] Every `uses:` pinned to a full commit SHA
- [ ] Each red gate diagnosed from its real log, not assumed to be a finding
- [ ] `make check` green on the committed tree, including 100% coverage
- [ ] `CHANGELOG.md` `[Unreleased]` has a `### Security` entry naming the advisories, so a patch release can be cut
- [ ] PR checks watched to completion, and Snyk/Sonar confirmed to have actually scanned
