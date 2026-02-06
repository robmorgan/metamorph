#!/bin/bash
set -e

git clone /upstream /workspace/repo
cd /workspace/repo
git config user.name "agent-${AGENT_ID}"
git config user.email "agent-${AGENT_ID}@metamorph.local"

SESSION=0
while true; do
  SESSION=$((SESSION + 1))
  LOG_FILE="/workspace/logs/session-${SESSION}.log"
  echo "[$(date)] Starting session $SESSION as ${AGENT_ROLE}" | tee -a "$LOG_FILE"

  git pull --rebase origin main 2>&1 | tee -a "$LOG_FILE"

  envsubst < AGENT_PROMPT.md > /tmp/agent_prompt.md

  claude --dangerously-skip-permissions \
    --model "${AGENT_MODEL}" \
    -p "$(cat /tmp/agent_prompt.md)" \
    2>&1 | tee -a "$LOG_FILE" || true

  echo "[$(date)] Session $SESSION ended, restarting in 5s..." | tee -a "$LOG_FILE"
  sleep 5
done
