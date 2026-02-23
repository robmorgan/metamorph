"use client"

import { useTask } from "@/hooks/use-api"
import { StatusBadge } from "./status-badge"
import { TaskPlanner } from "./task-planner"
import { AgentMonitor } from "./agent-monitor"
import {
  ArrowLeft,
  GitBranch,
  ExternalLink,
  Play,
  Pause,
  RotateCcw,
  Loader2,
} from "lucide-react"
import { Button } from "@/components/ui/button"
import { useState } from "react"
import { cn } from "@/lib/utils"

interface TaskDetailProps {
  taskId: string
  onBack: () => void
}

export function TaskDetail({ taskId, onBack }: TaskDetailProps) {
  const { data: task, loading, error } = useTask(taskId)
  const [activeTab, setActiveTab] = useState<"subtasks" | "agents">("subtasks")

  if (loading && !task) {
    return (
      <div className="flex flex-1 items-center justify-center">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    )
  }

  if (error || !task) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-2">
        <p className="text-sm text-destructive">Failed to load task: {error || "Not found"}</p>
        <Button variant="outline" size="sm" onClick={onBack}>Go back</Button>
      </div>
    )
  }

  return (
    <div className="flex flex-1 flex-col">
      {/* Top bar */}
      <div className="flex items-center gap-4 border-b border-border px-6 py-4">
        <button
          onClick={onBack}
          className="flex items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
        >
          <ArrowLeft className="h-4 w-4" />
          Tasks
        </button>
        <span className="text-muted-foreground">/</span>
        <span className="text-sm font-medium text-foreground font-mono">{task.name}</span>
        <StatusBadge status={task.status} />

        <div className="ml-auto flex items-center gap-2">
          {task.repo && (
            <span
              className="flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-xs font-medium text-muted-foreground"
            >
              <GitBranch className="h-3 w-3" />
              <span className="font-mono">{task.branch || task.repo}</span>
            </span>
          )}
          <Button variant="outline" size="sm" className="gap-1.5">
            <RotateCcw className="h-3 w-3" />
            Re-plan
          </Button>
          {task.status === "active" ? (
            <Button variant="outline" size="sm" className="gap-1.5">
              <Pause className="h-3 w-3" />
              Pause
            </Button>
          ) : (
            <Button size="sm" className="gap-1.5">
              <Play className="h-3 w-3" />
              Resume
            </Button>
          )}
        </div>
      </div>

      {/* Tabs */}
      <div className="flex gap-0 border-b border-border px-6">
        {[
          { id: "subtasks" as const, label: "Subtasks" },
          { id: "agents" as const, label: "Agents" },
        ].map((tab) => (
          <button
            key={tab.id}
            onClick={() => setActiveTab(tab.id)}
            className={cn(
              "relative px-4 py-3 text-sm font-medium transition-colors",
              activeTab === tab.id
                ? "text-foreground"
                : "text-muted-foreground hover:text-foreground"
            )}
          >
            {tab.label}
            {activeTab === tab.id && (
              <div className="absolute bottom-0 left-0 right-0 h-0.5 bg-primary" />
            )}
          </button>
        ))}
      </div>

      {/* Content */}
      <div className="flex-1 overflow-y-auto p-6">
        {activeTab === "subtasks" ? <TaskPlanner /> : <AgentMonitor />}
      </div>
    </div>
  )
}
