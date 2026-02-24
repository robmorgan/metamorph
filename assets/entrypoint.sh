#!/bin/bash
set -e

# Clone upstream repos into /workspace/.
# Multi-repo: /upstream/{label}/* → /workspace/{label}/
# Single-repo (legacy): /upstream/ is a bare repo → /workspace/repo/
FIRST_REPO=""
if ls /upstream/*/HEAD >/dev/null 2>&1; then
  # Multi-repo layout: /upstream/{label}/ each contain a bare repo.
  for upstream in /upstream/*/; do
    label=$(basename "$upstream")
    git clone "$upstream" "/workspace/$label"
    cd "/workspace/$label"
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
    if [ -z "$FIRST_REPO" ]; then FIRST_REPO="/workspace/$label"; fi
  done
else
  # Single-repo (legacy): /upstream is the bare repo itself.
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
  FIRST_REPO="/workspace/repo"
fi

cd "$FIRST_REPO"

SESSION=0
while true; do
  SESSION=$((SESSION + 1))
  LOG_FILE="/workspace/logs/session-${SESSION}.log"
  echo "[$(date)] Starting session $SESSION as ${AGENT_ROLE}" | tee -a "$LOG_FILE"

  # Pull latest changes for all repos.
  for repo_dir in /workspace/*/; do
    [ -d "$repo_dir/.git" ] || continue
    cd "$repo_dir"
    git pull --rebase origin HEAD 2>&1 | tee -a "$LOG_FILE" || true
  done
  cd "$FIRST_REPO"

  # --- Task queue mode (when CONTROL_PLANE_URL is set) ---
  if [ -n "$CONTROL_PLANE_URL" ]; then
    echo "[$(date)] Polling for tasks from control plane..." | tee -a "$LOG_FILE"

    TASK_RESPONSE=$(curl -sf -X POST "${CONTROL_PLANE_URL}/api/tasks/next" \
      -H "Content-Type: application/json" \
      -d "{\"agent_id\":${AGENT_ID},\"role\":\"${AGENT_ROLE}\",\"project\":\"${PROJECT_NAME}\"}" \
      -w "\n%{http_code}" 2>/dev/null) || TASK_RESPONSE=$'\n000'

    HTTP_CODE=$(echo "$TASK_RESPONSE" | tail -1)
    TASK_BODY=$(echo "$TASK_RESPONSE" | sed '$d')

    if [ "$HTTP_CODE" = "204" ] || [ "$HTTP_CODE" = "000" ] || [ -z "$TASK_BODY" ]; then
      echo "[$(date)] No tasks available, sleeping 10s..." | tee -a "$LOG_FILE"
      sleep 10
      continue
    fi

    if [ "$HTTP_CODE" != "200" ]; then
      echo "[$(date)] ERROR: control plane returned HTTP $HTTP_CODE" | tee -a "$LOG_FILE"
      sleep 10
      continue
    fi

    # Parse task JSON.
    TASK_ID=$(echo "$TASK_BODY" | jq -r '.id // empty')
    TASK_TITLE=$(echo "$TASK_BODY" | jq -r '.title // empty')
    TASK_DESC=$(echo "$TASK_BODY" | jq -r '.description // empty')

    if [ -z "$TASK_ID" ]; then
      echo "[$(date)] ERROR: failed to parse task from response" | tee -a "$LOG_FILE"
      sleep 10
      continue
    fi

    echo "[$(date)] Claimed task $TASK_ID: $TASK_TITLE" | tee -a "$LOG_FILE"

    # Build prompt: system prompt + task details + agent prompt.
    TASK_HEADER="
## Your Current Task
- **Task ID**: ${TASK_ID}
- **Title**: ${TASK_TITLE}
- **Description**: ${TASK_DESC}

Focus on completing this specific task. Plan your approach, implement the solution, and write tests.
When done, commit your changes with the task ID in the commit message.
"
    SYSTEM_PROMPT=$(cat /SYSTEM_PROMPT.md | envsubst)
    AGENT_PROMPT=""
    if [ -f /workspace/AGENT_PROMPT.md ]; then
      AGENT_PROMPT=$(cat /workspace/AGENT_PROMPT.md)
    fi
    FULL_PROMPT="${SYSTEM_PROMPT}
${TASK_HEADER}
${AGENT_PROMPT}"

    echo "[$(date)] Launching Claude Code for task $TASK_ID (model: ${AGENT_MODEL})..." | tee -a "$LOG_FILE"

    SESSION_START=$(date +%s)

    claude --dangerously-skip-permissions \
      --model "${AGENT_MODEL}" \
      --output-format stream-json \
      --verbose \
      -p "$FULL_PROMPT" \
      2>&1 | tee -a "$LOG_FILE" || true

    SESSION_END=$(date +%s)
    SESSION_DURATION=$((SESSION_END - SESSION_START))

    # Auto-commit and push for all repos.
    for repo_dir in /workspace/*/; do
      [ -d "$repo_dir/.git" ] || continue
      cd "$repo_dir"

      if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
        echo "[$(date)] Auto-committing changes for task $TASK_ID in $(basename "$repo_dir")..." | tee -a "$LOG_FILE"
        git add -A 2>&1 | tee -a "$LOG_FILE"
        git commit -m "metamorph: auto-commit for task $TASK_ID from session $SESSION" 2>&1 | tee -a "$LOG_FILE" || true
      fi

      if ! git push origin HEAD 2>&1 | tee -a "$LOG_FILE"; then
        echo "[$(date)] Push failed in $(basename "$repo_dir"), pulling and retrying..." | tee -a "$LOG_FILE"
        if ! git pull --rebase origin HEAD 2>&1 | tee -a "$LOG_FILE"; then
          echo "[$(date)] Rebase conflict, aborting rebase and falling back to merge..." | tee -a "$LOG_FILE"
          git rebase --abort 2>&1 | tee -a "$LOG_FILE" || true
          git pull --no-rebase -X theirs origin HEAD 2>&1 | tee -a "$LOG_FILE" || true
        fi
        git push origin HEAD 2>&1 | tee -a "$LOG_FILE" || true
      fi
    done
    cd "$FIRST_REPO"

    # Mark task complete.
    echo "[$(date)] Marking task $TASK_ID as complete..." | tee -a "$LOG_FILE"
    curl -sf -X POST "${CONTROL_PLANE_URL}/api/tasks/${TASK_ID}/complete" \
      -H "Content-Type: application/json" \
      -d "{\"summary\":\"Completed by agent-${AGENT_ID} in session ${SESSION} (${SESSION_DURATION}s)\"}" \
      2>&1 | tee -a "$LOG_FILE" || true

    if [ "$SESSION_DURATION" -lt 30 ]; then
      echo "[$(date)] Session lasted ${SESSION_DURATION}s (possible rate limit), backing off 300s..." | tee -a "$LOG_FILE"
      sleep 300
    else
      echo "[$(date)] Task $TASK_ID complete, next poll in 5s..." | tee -a "$LOG_FILE"
      sleep 5
    fi

    continue
  fi

  # --- Static prompt mode (fallback when CONTROL_PLANE_URL is unset) ---
  if [ -f /workspace/AGENT_PROMPT.md ]; then
    cat /SYSTEM_PROMPT.md /workspace/AGENT_PROMPT.md | envsubst > /tmp/AGENT_PROMPT.md
  else
    cat /SYSTEM_PROMPT.md | envsubst > /tmp/AGENT_PROMPT.md
  fi

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

  # Auto-commit and push for all repos.
  for repo_dir in /workspace/*/; do
    [ -d "$repo_dir/.git" ] || continue
    cd "$repo_dir"

    # Auto-commit any uncommitted changes left by the agent.
    if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
      echo "[$(date)] Auto-committing uncommitted changes in $(basename "$repo_dir") from session $SESSION..." | tee -a "$LOG_FILE"
      git add -A 2>&1 | tee -a "$LOG_FILE"
      git commit -m "metamorph: auto-commit uncommitted changes from session $SESSION" 2>&1 | tee -a "$LOG_FILE" || true
    fi

    # Push any commits the agent made during this session.
    if ! git push origin HEAD 2>&1 | tee -a "$LOG_FILE"; then
      echo "[$(date)] Push failed in $(basename "$repo_dir"), pulling and retrying..." | tee -a "$LOG_FILE"
      if ! git pull --rebase origin HEAD 2>&1 | tee -a "$LOG_FILE"; then
        echo "[$(date)] Rebase conflict, aborting rebase and falling back to merge..." | tee -a "$LOG_FILE"
        git rebase --abort 2>&1 | tee -a "$LOG_FILE" || true
        git pull --no-rebase -X theirs origin HEAD 2>&1 | tee -a "$LOG_FILE" || true
      fi
      git push origin HEAD 2>&1 | tee -a "$LOG_FILE" || true
    fi
  done
  cd "$FIRST_REPO"

  if [ "$SESSION_DURATION" -lt 30 ]; then
    echo "[$(date)] Session lasted ${SESSION_DURATION}s (possible rate limit), backing off 300s..." | tee -a "$LOG_FILE"
    sleep 300
  else
    echo "[$(date)] Session $SESSION ended, restarting in 5s..." | tee -a "$LOG_FILE"
    sleep 5
  fi
done
