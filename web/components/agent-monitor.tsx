"use client"

import { mockAgents } from "@/lib/mock-data"
import { StatusBadge } from "./status-badge"
import { cn } from "@/lib/utils"
import { Bot, Hash, Clock } from "lucide-react"
import type { Agent } from "@/lib/types"

function AgentCard({ agent }: { agent: Agent }) {
  return (
    <div className={cn(
      "rounded-lg border bg-card",
      agent.status === "running" ? "border-primary/30" : "border-border"
    )}>
      <div className="flex items-center gap-4 px-4 py-3">
        <div className={cn(
          "flex h-9 w-9 items-center justify-center rounded-lg",
          agent.status === "running" ? "bg-primary/10" : "bg-secondary"
        )}>
          <Bot className={cn(
            "h-4 w-4",
            agent.status === "running" ? "text-primary" : "text-muted-foreground"
          )} />
        </div>
        <div className="flex flex-1 flex-col gap-0.5">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium text-foreground font-mono">agent-{agent.id}</span>
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
      </div>

      {/* Metrics */}
      <div className="grid grid-cols-2 gap-px border-t border-border bg-border">
        <div className="bg-card px-4 py-2">
          <div className="flex items-center gap-1.5 text-[10px] uppercase tracking-wider text-muted-foreground">
            <Hash className="h-3 w-3" />
            Sessions
          </div>
          <div className="mt-0.5 text-xs font-medium text-foreground font-mono">
            {agent.sessionsCompleted}
          </div>
        </div>
        <div className="bg-card px-4 py-2">
          <div className="flex items-center gap-1.5 text-[10px] uppercase tracking-wider text-muted-foreground">
            <Clock className="h-3 w-3" />
            Last Active
          </div>
          <div className="mt-0.5 text-xs font-medium text-foreground font-mono">
            {agent.lastActivity
              ? new Date(agent.lastActivity).toLocaleTimeString()
              : "—"}
          </div>
        </div>
      </div>
    </div>
  )
}

export function AgentMonitor() {
  const runningCount = mockAgents.filter((a) => a.status === "running").length

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <div>
          <h3 className="text-sm font-semibold text-foreground">Agents</h3>
          <p className="text-xs text-muted-foreground">
            {runningCount} of {mockAgents.length} agents running
          </p>
        </div>
      </div>

      <div className="flex flex-col gap-3">
        {mockAgents.map((agent) => (
          <AgentCard key={agent.id} agent={agent} />
        ))}
      </div>
    </div>
  )
}
