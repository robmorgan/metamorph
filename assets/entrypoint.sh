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

  git pull --rebase origin HEAD 2>&1 | tee -a "$LOG_FILE" || true

  cat /SYSTEM_PROMPT.md /workspace/AGENT_PROMPT.md | envsubst > /tmp/AGENT_PROMPT.md

  echo "[$(date)] Launching Claude Code (model: ${AGENT_MODEL})..." | tee -a "$LOG_FILE"

  SESSION_START=$(date +%s)

  claude --dangerously-skip-permissions \
    --model "${AGENT_MODEL}" \
    --output-format stream-json \
    --verbose \
    -p "$(cat /tmp/AGENT_PROMPT.md)" \
    2>&1 | tee -a "$LOG_FILE" || true

  SESSION_END=$(date +%s)
  SESSION_DURATION=$((SESSION_END - SESSION_START))

  # Auto-commit any uncommitted changes left by the agent.
  if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
    echo "[$(date)] Auto-committing uncommitted changes from session $SESSION..." | tee -a "$LOG_FILE"
    git add -A 2>&1 | tee -a "$LOG_FILE"
    git commit -m "metamorph: auto-commit uncommitted changes from session $SESSION" 2>&1 | tee -a "$LOG_FILE" || true
  fi

  # Push any commits the agent made during this session.
  if ! git push origin HEAD 2>&1 | tee -a "$LOG_FILE"; then
    echo "[$(date)] Push failed, pulling and retrying..." | tee -a "$LOG_FILE"
    git pull --rebase origin HEAD 2>&1 | tee -a "$LOG_FILE" || true
    git push origin HEAD 2>&1 | tee -a "$LOG_FILE" || true
  fi

  if [ "$SESSION_DURATION" -lt 30 ]; then
    echo "[$(date)] Session lasted ${SESSION_DURATION}s (possible rate limit), backing off 300s..." | tee -a "$LOG_FILE"
    sleep 300
  else
    echo "[$(date)] Session $SESSION ended, restarting in 5s..." | tee -a "$LOG_FILE"
    sleep 5
  fi
done
