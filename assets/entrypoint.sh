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

  # Stash any uncommitted changes from the previous session
  STASH_RESULT=$(git stash --include-untracked 2>&1)
  echo "$STASH_RESULT" | tee -a "$LOG_FILE"

  git pull --rebase origin HEAD 2>&1 | tee -a "$LOG_FILE" || true

  # Restore stashed changes if any were stashed
  if echo "$STASH_RESULT" | grep -q "Saved working directory"; then
    git stash pop 2>&1 | tee -a "$LOG_FILE" || true
  fi

  cat /SYSTEM_PROMPT.md /workspace/AGENT_PROMPT.md | envsubst > /tmp/AGENT_PROMPT.md

  echo "[$(date)] Launching Claude Code (model: ${AGENT_MODEL})..." | tee -a "$LOG_FILE"

  SESSION_START=$(date +%s)

  claude --dangerously-skip-permissions \
    --model "${AGENT_MODEL}" \
    --output-format stream-json \
    -p "$(cat /tmp/AGENT_PROMPT.md)" \
    2>&1 | tee -a "$LOG_FILE" || true

  SESSION_END=$(date +%s)
  SESSION_DURATION=$((SESSION_END - SESSION_START))

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
