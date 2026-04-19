#!/usr/bin/env bash
# Block `git commit` on main (any session) and `git push` from sub-agents.
#
# Flow this enforces:
#   - Everyone works on a feature branch: `git checkout -b <slug>`
#   - Sub-agents commit there; the dispatcher merges to main and pushes.
#   - Direct commits to main are disabled to prevent two parallel sessions
#     (or an agent + dispatcher) from stomping each other.
set -eu

input=$(cat)
cmd=$(printf '%s' "$input" | jq -r '.tool_input.command // empty' 2>/dev/null || echo "")
repo_root="${CLAUDE_PROJECT_DIR:-/home/shuki/projects/jabali2}"
branch=$(git -C "$repo_root" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "unknown")
agent="${CLAUDE_AGENT_NAME:-}"

case "$cmd" in
    *"git commit"*)
        if [ "$branch" = "main" ] || [ "$branch" = "master" ]; then
            if [ -n "$agent" ]; then
                cat >&2 <<EOF
BLOCKED: sub-agent "$agent" attempted \`$cmd\` on branch "$branch".
Per CLAUDE.md rule 5, sub-agents must work on a feature branch:
  git checkout -b <wave-or-task-slug>
The dispatcher reviews the branch before merging to main.
EOF
            else
                cat >&2 <<EOF
BLOCKED: commits to "$branch" are disabled to prevent parallel-session stomping.
Workflow:
  git checkout -b <slug>
  # make changes, commit
  git checkout main && git merge <slug>
  git push
(This hook lives at .claude/hooks/block-agent-commit-main.sh — edit or disable
via /hooks if the guard is in your way.)
EOF
            fi
            exit 2
        fi
        ;;
    *"git push"*)
        if [ -n "$agent" ]; then
            cat >&2 <<EOF
BLOCKED: sub-agent "$agent" attempted \`$cmd\`.
Per CLAUDE.md rule 5, only the dispatcher pushes. Commit to the feature branch and report the SHAs back.
EOF
            exit 2
        fi
        ;;
esac
exit 0
