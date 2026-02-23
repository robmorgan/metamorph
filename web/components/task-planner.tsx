"use client"

import { mockSubtasks } from "@/lib/mock-data"
import { StatusBadge } from "./status-badge"
import { cn } from "@/lib/utils"
import {
  ChevronDown,
  ChevronRight,
  Clock,
  CheckCircle2,
  Circle,
  Loader2,
  AlertTriangle,
  Ban,
} from "lucide-react"
import { useState } from "react"
import type { Subtask, SubtaskStatus } from "@/lib/types"

const statusIcons: Record<SubtaskStatus, React.ReactNode> = {
  completed: <CheckCircle2 className="h-4 w-4 text-success" />,
  "in-progress": <Loader2 className="h-4 w-4 animate-spin text-info" />,
  pending: <Circle className="h-4 w-4 text-muted-foreground" />,
  failed: <AlertTriangle className="h-4 w-4 text-destructive" />,
  blocked: <Ban className="h-4 w-4 text-warning" />,
}

function SubtaskItem({ subtask }: { subtask: Subtask }) {
  const [expanded, setExpanded] = useState(subtask.status === "in-progress")

  return (
    <div className="rounded-lg border border-border bg-card">
      <button
        onClick={() => setExpanded(!expanded)}
        className="flex w-full items-center gap-3 px-4 py-3 text-left"
      >
        <span className="text-muted-foreground">
          {expanded ? (
            <ChevronDown className="h-4 w-4" />
          ) : (
            <ChevronRight className="h-4 w-4" />
          )}
        </span>
        {statusIcons[subtask.status]}
        <div className="flex flex-1 flex-col gap-0.5 overflow-hidden">
          <span className="truncate text-sm font-medium text-foreground">
            {subtask.title}
          </span>
          <span className="truncate text-xs text-muted-foreground">
            {subtask.description}
          </span>
        </div>
        <StatusBadge status={subtask.status} />
        {subtask.duration && (
          <span className="flex items-center gap-1 text-xs text-muted-foreground font-mono">
            <Clock className="h-3 w-3" />
            {subtask.duration}
          </span>
        )}
      </button>

      {expanded && (
        <div className="border-t border-border px-4 py-3">
          <div className="flex flex-col gap-2 pl-7">
            {subtask.steps.map((step, idx) => (
              <div
                key={step.id}
                className={cn(
                  "flex items-center gap-3 rounded-md px-3 py-2 text-sm",
                  step.status === "in-progress" && "bg-info/5 border border-info/20"
                )}
              >
                <div className="flex h-5 w-5 items-center justify-center text-xs text-muted-foreground font-mono">
                  {idx + 1}
                </div>
                {statusIcons[step.status]}
                <span
                  className={cn(
                    "flex-1",
                    step.status === "completed"
                      ? "text-muted-foreground"
                      : "text-foreground"
                  )}
                >
                  {step.title}
                </span>
                {step.duration && (
                  <span className="text-xs text-muted-foreground font-mono">
                    {step.duration}
                  </span>
                )}
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

export function TaskPlanner() {
  const completedCount = mockSubtasks.filter(
    (s) => s.status === "completed"
  ).length
  const totalCount = mockSubtasks.length
  const progressPct = (completedCount / totalCount) * 100

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <div>
          <h3 className="text-sm font-semibold text-foreground">Subtasks</h3>
          <p className="text-xs text-muted-foreground">
            {completedCount} of {totalCount} subtasks completed
          </p>
        </div>
        <div className="flex items-center gap-3">
          <div className="h-2 w-32 overflow-hidden rounded-full bg-secondary">
            <div
              className="h-full rounded-full bg-primary transition-all"
              style={{ width: `${progressPct}%` }}
            />
          </div>
          <span className="text-xs font-medium text-foreground font-mono">
            {Math.round(progressPct)}%
          </span>
        </div>
      </div>

      <div className="flex flex-col gap-2">
        {mockSubtasks.map((subtask) => (
          <SubtaskItem key={subtask.id} subtask={subtask} />
        ))}
      </div>
    </div>
  )
}
