"use client"

import { useTasks } from "@/hooks/use-api"
import { StatusBadge } from "./status-badge"
import { Plus, Search, GitBranch, ListChecks, Activity, Loader2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { useState } from "react"

const priorityLabels: Record<number, string> = {
  0: "Critical",
  1: "High",
  2: "Medium",
  3: "Low",
  4: "Backlog",
}

interface TasksDashboardProps {
  onSelectTask: (taskId: string) => void
  onNewTask: () => void
}

export function TasksDashboard({ onSelectTask, onNewTask }: TasksDashboardProps) {
  const [search, setSearch] = useState("")
  const [filter, setFilter] = useState<string>("all")

  const { data: tasks, loading, error } = useTasks()
  const taskList = tasks ?? []

  const filtered = taskList.filter((t) => {
    const matchesSearch =
      t.name.toLowerCase().includes(search.toLowerCase()) ||
      t.description.toLowerCase().includes(search.toLowerCase())
    const matchesFilter = filter === "all" || t.status === filter
    return matchesSearch && matchesFilter
  })

  const stats = {
    total: taskList.length,
    active: taskList.filter((t) => t.status === "active").length,
    completed: taskList.filter((t) => t.status === "completed").length,
    errors: taskList.filter((t) => t.status === "error").length,
  }

  if (loading && !tasks) {
    return (
      <div className="flex flex-1 items-center justify-center p-6">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    )
  }

  if (error && !tasks) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-2 p-6">
        <p className="text-sm text-destructive">Failed to load tasks: {error}</p>
        <p className="text-xs text-muted-foreground">Make sure the daemon is running on port 8377</p>
      </div>
    )
  }

  return (
    <div className="flex flex-1 flex-col gap-6 p-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight text-foreground">Tasks</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Define tasks and let agents automatically break them into subtasks for execution
          </p>
        </div>
        <Button onClick={onNewTask} className="gap-2">
          <Plus className="h-4 w-4" />
          New Task
        </Button>
      </div>

      {/* Stats */}
      <div className="grid grid-cols-4 gap-4">
        {[
          { label: "Total Tasks", value: stats.total, icon: ListChecks },
          { label: "Active", value: stats.active, icon: Activity },
          { label: "Completed", value: stats.completed, icon: ListChecks },
          { label: "Errors", value: stats.errors, icon: Activity },
        ].map((stat) => (
          <div key={stat.label} className="rounded-lg border border-border bg-card p-4">
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <stat.icon className="h-3.5 w-3.5" />
              {stat.label}
            </div>
            <div className="mt-2 text-2xl font-semibold text-foreground">{stat.value}</div>
          </div>
        ))}
      </div>

      {/* Filter bar */}
      <div className="flex items-center gap-3">
        <div className="relative flex-1">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            placeholder="Search tasks..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="pl-9"
          />
        </div>
        <div className="flex gap-1 rounded-lg border border-border bg-card p-1">
          {["all", "active", "completed", "error"].map((f) => (
            <button
              key={f}
              onClick={() => setFilter(f)}
              className={`rounded-md px-3 py-1.5 text-xs font-medium capitalize transition-colors ${
                filter === f
                  ? "bg-accent text-foreground"
                  : "text-muted-foreground hover:text-foreground"
              }`}
            >
              {f}
            </button>
          ))}
        </div>
      </div>

      {/* Tasks list */}
      <div className="flex flex-col gap-2">
        <div className="grid grid-cols-[1fr_120px_140px_100px_100px] items-center gap-4 px-4 py-2 text-xs font-medium uppercase tracking-wider text-muted-foreground">
          <span>Task</span>
          <span>Status</span>
          <span>Repository</span>
          <span>Priority</span>
          <span>Created</span>
        </div>
        {filtered.length === 0 ? (
          <div className="flex flex-col items-center justify-center rounded-lg border border-dashed border-border py-12 text-center">
            <ListChecks className="h-8 w-8 text-muted-foreground" />
            <p className="mt-3 text-sm font-medium text-foreground">No tasks found</p>
            <p className="mt-1 text-xs text-muted-foreground">
              {taskList.length === 0
                ? "Create a task to get started"
                : "Try adjusting your search or filter"}
            </p>
          </div>
        ) : (
          filtered.map((task) => (
            <button
              key={task.id}
              onClick={() => onSelectTask(task.id)}
              className="grid grid-cols-[1fr_120px_140px_100px_100px] items-center gap-4 rounded-lg border border-border bg-card px-4 py-3 text-left transition-colors hover:bg-accent"
            >
              <div className="flex flex-col gap-0.5 overflow-hidden">
                <span className="truncate text-sm font-medium text-foreground font-mono">
                  {task.name}
                </span>
                <span className="truncate text-xs text-muted-foreground">{task.description}</span>
              </div>
              <StatusBadge status={task.status} />
              <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
                {task.repo && (
                  <>
                    <GitBranch className="h-3 w-3 shrink-0" />
                    <span className="truncate font-mono">{task.repo}</span>
                  </>
                )}
              </div>
              <span className="text-xs text-muted-foreground">
                {priorityLabels[task.priority] ?? `P${task.priority}`}
              </span>
              <span className="text-xs text-muted-foreground">
                {task.createdAt ? new Date(task.createdAt).toLocaleDateString() : ""}
              </span>
            </button>
          ))
        )}
      </div>
    </div>
  )
}
