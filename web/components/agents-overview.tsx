"use client"

import { useProjects, useProjectState } from "@/hooks/use-api"
import { StatusBadge } from "./status-badge"
import { cn } from "@/lib/utils"
import { Bot, Hash, Clock, Loader2 } from "lucide-react"
import { useState } from "react"

export function AgentsOverview() {
  const { data: projects, loading: projectsLoading } = useProjects()
  const [selectedProject, setSelectedProject] = useState<string | null>(null)

  // Auto-select first project if none selected.
  const activeProject = selectedProject || (projects && projects.length > 0 ? projects[0].name : null)
  const { data: agents, loading: agentsLoading, error } = useProjectState(activeProject)

  const agentList = agents ?? []
  const projectList = projects ?? []

  if (projectsLoading && !projects) {
    return (
      <div className="flex flex-1 items-center justify-center p-6">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    )
  }

  return (
    <div className="flex flex-1 flex-col gap-6 p-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight text-foreground">Agents</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Monitor all active agents across your projects
        </p>
      </div>

      {/* Project selector */}
      {projectList.length > 1 && (
        <div className="flex gap-1 rounded-lg border border-border bg-card p-1">
          {projectList.map((p) => (
            <button
              key={p.name}
              onClick={() => setSelectedProject(p.name)}
              className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
                activeProject === p.name
                  ? "bg-accent text-foreground"
                  : "text-muted-foreground hover:text-foreground"
              }`}
            >
              {p.name}
            </button>
          ))}
        </div>
      )}

      <div className="grid grid-cols-3 gap-4">
        <div className="rounded-lg border border-border bg-card p-4">
          <div className="text-xs text-muted-foreground">Total Agents</div>
          <div className="mt-2 text-2xl font-semibold text-foreground">{agentList.length}</div>
        </div>
        <div className="rounded-lg border border-border bg-card p-4">
          <div className="text-xs text-muted-foreground">Running</div>
          <div className="mt-2 text-2xl font-semibold text-primary">
            {agentList.filter((a) => a.status === "running").length}
          </div>
        </div>
        <div className="rounded-lg border border-border bg-card p-4">
          <div className="text-xs text-muted-foreground">Sessions Completed</div>
          <div className="mt-2 text-2xl font-semibold text-foreground font-mono">
            {agentList.reduce((sum, a) => sum + a.sessionsCompleted, 0)}
          </div>
        </div>
      </div>

      {error && agentList.length === 0 && (
        <div className="rounded-lg border border-dashed border-border py-8 text-center">
          <Bot className="mx-auto h-8 w-8 text-muted-foreground" />
          <p className="mt-3 text-sm text-muted-foreground">
            {activeProject
              ? "No agent state available. Start the project to see agents."
              : "Select a project to view agents"}
          </p>
        </div>
      )}

      <div className="flex flex-col gap-3">
        {agentList.map((agent) => (
          <div
            key={agent.id}
            className={cn(
              "rounded-lg border bg-card",
              agent.status === "running" ? "border-primary/30" : "border-border"
            )}
          >
            <div className="flex items-center gap-4 px-5 py-4">
              <div className={cn(
                "flex h-10 w-10 items-center justify-center rounded-lg",
                agent.status === "running" ? "bg-primary/10" : "bg-secondary"
              )}>
                <Bot className={cn(
                  "h-5 w-5",
                  agent.status === "running" ? "text-primary" : "text-muted-foreground"
                )} />
              </div>
              <div className="flex flex-1 flex-col gap-1">
                <div className="flex items-center gap-2">
                  <span className="text-sm font-semibold text-foreground font-mono">
                    agent-{agent.id}
                  </span>
                  <span className="rounded bg-secondary px-1.5 py-0.5 text-[10px] text-muted-foreground">
                    {agent.role}
                  </span>
                  <StatusBadge status={agent.status} />
                </div>
                {agent.currentTask && (
                  <span className="text-xs text-muted-foreground">
                    Working on: {agent.currentTask}
                  </span>
                )}
              </div>
              <div className="flex items-center gap-6 text-xs text-muted-foreground">
                <div className="flex items-center gap-1.5">
                  <Hash className="h-3.5 w-3.5" />
                  <span className="font-mono">{agent.sessionsCompleted} sessions</span>
                </div>
                <div className="flex items-center gap-1.5">
                  <Clock className="h-3.5 w-3.5" />
                  <span className="font-mono">
                    {agent.lastActivity
                      ? new Date(agent.lastActivity).toLocaleTimeString()
                      : "—"}
                  </span>
                </div>
              </div>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
