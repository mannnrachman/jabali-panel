<!-- gitnexus:start -->
# GitNexus — Code Intelligence

This project is indexed by GitNexus as **jabali2** (10073 symbols, 21244 relationships, 299 execution flows). Use the GitNexus MCP tools to understand code, assess impact, and navigate safely.

> If any GitNexus tool warns the index is stale, run `npx gitnexus analyze` in terminal first.

## Always Do

- **MUST run impact analysis before editing any symbol.** Before modifying a function, class, or method, run `gitnexus_impact({target: "symbolName", direction: "upstream"})` and report the blast radius (direct callers, affected processes, risk level) to the user.
- **MUST run `gitnexus_detect_changes()` before committing** to verify your changes only affect expected symbols and execution flows.
- **MUST warn the user** if impact analysis returns HIGH or CRITICAL risk before proceeding with edits.
- When exploring unfamiliar code, use `gitnexus_query({query: "concept"})` to find execution flows instead of grepping. It returns process-grouped results ranked by relevance.
- When you need full context on a specific symbol — callers, callees, which execution flows it participates in — use `gitnexus_context({name: "symbolName"})`.

## Never Do

- NEVER edit a function, class, or method without first running `gitnexus_impact` on it.
- NEVER ignore HIGH or CRITICAL risk warnings from impact analysis.
- NEVER rename symbols with find-and-replace — use `gitnexus_rename` which understands the call graph.
- NEVER commit changes without running `gitnexus_detect_changes()` to check affected scope.

## Resources

| Resource | Use for |
|----------|---------|
| `gitnexus://repo/jabali2/context` | Codebase overview, check index freshness |
| `gitnexus://repo/jabali2/clusters` | All functional areas |
| `gitnexus://repo/jabali2/processes` | All execution flows |
| `gitnexus://repo/jabali2/process/{name}` | Step-by-step execution trace |

## CLI

| Task | Read this skill file |
|------|---------------------|
| Understand architecture / "How does X work?" | `.claude/skills/gitnexus/gitnexus-exploring/SKILL.md` |
| Blast radius / "What breaks if I change X?" | `.claude/skills/gitnexus/gitnexus-impact-analysis/SKILL.md` |
| Trace bugs / "Why is X failing?" | `.claude/skills/gitnexus/gitnexus-debugging/SKILL.md` |
| Rename / extract / split / refactor | `.claude/skills/gitnexus/gitnexus-refactoring/SKILL.md` |
| Tools, resources, schema reference | `.claude/skills/gitnexus/gitnexus-guide/SKILL.md` |
| Index, status, clean, wiki CLI commands | `.claude/skills/gitnexus/gitnexus-cli/SKILL.md` |

<!-- gitnexus:end -->

<!-- ⚠️ The block above may use simplified tool names. The ACTUAL MCP tool
     identifiers are prefixed `mcp__gitnexus__`. Use the names in this
     section when calling tools. -->

## Actual tool names (use these — the `gitnexus_*` above are shorthand)

| Shorthand in docs | Real MCP tool name |
|---|---|
| `gitnexus_impact` | `mcp__gitnexus__impact` |
| `gitnexus_context` | `mcp__gitnexus__context` |
| `gitnexus_query` | `mcp__gitnexus__query` |
| `gitnexus_detect_changes` | `mcp__gitnexus__detect_changes` |
| `gitnexus_rename` | `mcp__gitnexus__rename` |
| Graph-augmented search | `mcp__gitnexus__cypher` (raw Cypher), `mcp__gitnexus__route_map`, `mcp__gitnexus__tool_map` |
| Multi-repo / groups | `mcp__gitnexus__group_*`, `mcp__gitnexus__list_repos` |

Tool schemas are deferred. If you need one, load it first:
`ToolSearch("select:mcp__gitnexus__impact,mcp__gitnexus__context,mcp__gitnexus__query,mcp__gitnexus__detect_changes")`.

## Sub-agent mandate (applies to every agent spawned in this project)

Any agent launched via the `Agent` tool in the jabali2 worktree MUST:

1. **Before first edit** to any `.go`, `.tsx`, `.ts` or migration file — call `mcp__gitnexus__impact` on the target symbol (function/struct/handler) and include the returned blast radius in its reasoning. If `impact` reports HIGH or CRITICAL risk, stop and report to the dispatcher BEFORE editing.

2. **Before committing** — call `mcp__gitnexus__detect_changes` and verify affected symbols match the intended scope. If the change set includes unexpected symbols, pause and report.

3. **For unfamiliar code exploration** (any "how does X work?" type question) — prefer `mcp__gitnexus__query` or `mcp__gitnexus__context` over `Grep`/`Read`. Text search is the fallback, not the default.

4. **For renames** — use `mcp__gitnexus__rename`, never find-and-replace across files.

5. **Agents commit to branches, never to `main`. Agents never `git push`.**
   - Before your first commit, create a feature branch: `git checkout -b <wave-or-task-slug>` (e.g. `wave-c-http-handlers`, `fix-ssl-badge`, `fix-impersonate-port`, `chore-update-deps`). Use a short, descriptive slug — no ticket numbers required.
   - If you arrive on `main` and need to commit, switch to a new branch first. Committing directly to `main` is a dispatch failure; the dispatcher will revert your work.
   - Never run `git push`, `git push origin`, `git push --force`, or anything that publishes to remote. The dispatcher is the ONLY entity that pushes, after independent verification and a deliberate merge to `main`.
   - Never run destructive git (`reset --hard`, `checkout --`, `clean -fd`, `branch -D`) outside your own feature branch — you can corrupt a concurrent agent's working tree.
   - **Before your final report, rebase onto latest `origin/main` and re-run tests.** This is a dispatch-check item, not optional. Concretely:
       ```
       git fetch origin main
       git rebase origin/main        # resolve any conflicts HERE, with full context
       # re-run the relevant test suite for your change
       ```
     Rationale: conflicts between your work and what landed on `main` during your session belong to you — you have the intent and the context. If the dispatcher resolves them at merge time, they're guessing with `grep`. Two recent incidents (wt-a catalog expansion vs applications.go, wt-a jabali-app CLI vs deleted `internal/clientapi`) shipped only because the worktree author hadn't rebased; both cost cycles and one came close to landing a half-working CLI. If your rebase is clean, merge to `main` is a fast-forward — no three-way merge, no guessing.
   - Your final report MUST include: the branch name, the commit SHAs on that branch, a `git log main..<your-branch>` summary, AND confirmation that you rebased onto the latest `origin/main` and re-ran tests post-rebase.

This applies to planner, coder, backend-dev, security-architect, Explore, and every other sub-agent type. When a wave brief asks you to modify a symbol, the dispatcher assumes you've run impact analysis first AND your work lives on a feature branch. Acknowledge both in your summary output.

If the gitnexus index is missing or stale, run `npx gitnexus analyze` once in the repo root and retry. Never skip the step silently.
