#!/bin/bash
set -e

git clone /upstream /workspace/repo
cd /workspace/repo
if [ -n "$GIT_AUTHOR_NAME" ]; then
  git config user.name "$GIT_AUTHOR_NAME"
else
  git config user.name "$(git log -1 --format='%an' 2>/dev/null || echo "agent-${AGENT_ID}")"
fi
if [ -n "$GIT_AUTHOR_EMAIL" ]; then
  git config user.email "$GIT_AUTHOR_EMAIL"
else
  git config user.email "$(git log -1 --format='%ae' 2>/dev/null || echo "agent-${AGENT_ID}@metamorph.local")"
fi

SESSION=0
while true; do
  SESSION=$((SESSION + 1))
  LOG_FILE="/workspace/logs/session-${SESSION}.log"
  echo "[$(date)] Starting session $SESSION as ${AGENT_ROLE}" | tee -a "$LOG_FILE"

  git pull --rebase origin main 2>&1 | tee -a "$LOG_FILE"

  cat /SYSTEM_PROMPT.md /workspace/AGENT_PROMPT.md | envsubst > /tmp/AGENT_PROMPT.md

  claude --dangerously-skip-permissions \
    --model "${AGENT_MODEL}" \
    -p "$(cat /tmp/AGENT_PROMPT.md)" \
    2>&1 | tee -a "$LOG_FILE" || true

  # Push any commits the agent made during this session.
  git push origin main 2>&1 | tee -a "$LOG_FILE" || true

  echo "[$(date)] Session $SESSION ended, restarting in 5s..." | tee -a "$LOG_FILE"
  sleep 5
done
