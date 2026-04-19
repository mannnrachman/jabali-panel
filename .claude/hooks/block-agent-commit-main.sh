#!/usr/bin/env bash
# Block `git commit` / `git push` on main when running inside a sub-agent.
# Main session (dispatcher) is unaffected: only fires when CLAUDE_AGENT_NAME is set.
set -eu

# If CLAUDE_AGENT_NAME is not set, we're the main session → allow.
if [ -z "${CLAUDE_AGENT_NAME:-}" ]; then
    exit 0
fi

input=$(cat)
cmd=$(printf '%s' "$input" | jq -r '.tool_input.command // empty' 2>/dev/null || echo "")

case "$cmd" in
    *"git commit"*|*"git push"*)
        repo_root="${CLAUDE_PROJECT_DIR:-/home/shuki/projects/jabali2}"
        branch=$(git -C "$repo_root" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "unknown")
        if [ "$branch" = "main" ] || [ "$branch" = "master" ]; then
            cat >&2 <<EOF
BLOCKED: sub-agent "${CLAUDE_AGENT_NAME}" attempted \`$cmd\` on branch "$branch".
Per CLAUDE.md rule 5, sub-agents must work on a feature branch:
  git checkout -b <wave-or-task-slug>
The dispatcher reviews the branch before merging to main.
EOF
            exit 2
        fi
        # push from a feature branch is still banned — dispatcher pushes only
        case "$cmd" in
            *"git push"*)
                cat >&2 <<EOF
BLOCKED: sub-agent "${CLAUDE_AGENT_NAME}" attempted \`git push\`.
Per CLAUDE.md rule 5, only the dispatcher pushes. Commit to the feature branch and report the SHAs back.
EOF
                exit 2
                ;;
        esac
        ;;
esac
exit 0
