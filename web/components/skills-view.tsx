"use client"

import { mockSkills } from "@/lib/mock-data"
import { cn } from "@/lib/utils"
import {
  Blocks,
  Code2,
  TestTube2,
  Rocket,
  ScanSearch,
  FileText,
  Clock,
  Hash,
} from "lucide-react"
import { Switch } from "@/components/ui/switch"
import { useState } from "react"

const categoryConfig: Record<
  string,
  { label: string; icon: React.ElementType; color: string }
> = {
  code: { label: "Code", icon: Code2, color: "text-primary" },
  test: { label: "Test", icon: TestTube2, color: "text-info" },
  deploy: { label: "Deploy", icon: Rocket, color: "text-success" },
  review: { label: "Review", icon: ScanSearch, color: "text-warning" },
  document: { label: "Docs", icon: FileText, color: "text-muted-foreground" },
}

export function SkillsView() {
  const [filter, setFilter] = useState<string>("all")
  const categories = ["all", ...Object.keys(categoryConfig)]

  const filtered =
    filter === "all"
      ? mockSkills
      : mockSkills.filter((s) => s.category === filter)

  return (
    <div className="flex flex-1 flex-col gap-6 p-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight text-foreground">
            Skills
          </h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Registry of capabilities agents can use during task execution
          </p>
        </div>
      </div>

      {/* Stats */}
      <div className="grid grid-cols-3 gap-4">
        <div className="rounded-lg border border-border bg-card p-4">
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <Blocks className="h-3.5 w-3.5" />
            Total Skills
          </div>
          <div className="mt-2 text-2xl font-semibold text-foreground">
            {mockSkills.length}
          </div>
        </div>
        <div className="rounded-lg border border-border bg-card p-4">
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <Blocks className="h-3.5 w-3.5" />
            Enabled
          </div>
          <div className="mt-2 text-2xl font-semibold text-primary">
            {mockSkills.filter((s) => s.enabled).length}
          </div>
        </div>
        <div className="rounded-lg border border-border bg-card p-4">
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <Hash className="h-3.5 w-3.5" />
            Total Invocations
          </div>
          <div className="mt-2 text-2xl font-semibold text-foreground font-mono">
            {mockSkills.reduce((sum, s) => sum + s.usageCount, 0)}
          </div>
        </div>
      </div>

      {/* Category filter */}
      <div className="flex gap-1 rounded-lg border border-border bg-card p-1 self-start">
        {categories.map((c) => {
          const cfg = categoryConfig[c]
          return (
            <button
              key={c}
              onClick={() => setFilter(c)}
              className={cn(
                "flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium capitalize transition-colors",
                filter === c
                  ? "bg-accent text-foreground"
                  : "text-muted-foreground hover:text-foreground"
              )}
            >
              {cfg && <cfg.icon className="h-3 w-3" />}
              {cfg?.label ?? "All"}
            </button>
          )
        })}
      </div>

      {/* Skills list */}
      <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
        {filtered.map((skill) => {
          const cfg = categoryConfig[skill.category]
          const Icon = cfg.icon
          return (
            <div
              key={skill.id}
              className={cn(
                "flex items-start gap-4 rounded-lg border bg-card p-4 transition-colors",
                skill.enabled ? "border-border" : "border-border opacity-50"
              )}
            >
              <div
                className={cn(
                  "flex h-10 w-10 shrink-0 items-center justify-center rounded-lg",
                  skill.enabled ? "bg-primary/10" : "bg-secondary"
                )}
              >
                <Icon
                  className={cn(
                    "h-5 w-5",
                    skill.enabled ? cfg.color : "text-muted-foreground"
                  )}
                />
              </div>
              <div className="flex flex-1 flex-col gap-1.5">
                <div className="flex items-center justify-between">
                  <span className="text-sm font-semibold text-foreground">
                    {skill.name}
                  </span>
                  <Switch defaultChecked={skill.enabled} />
                </div>
                <p className="text-xs leading-relaxed text-muted-foreground">
                  {skill.description}
                </p>
                <div className="flex items-center gap-4 pt-1 text-xs text-muted-foreground">
                  <span
                    className={cn(
                      "rounded-full px-2 py-0.5 text-[10px] font-medium",
                      "bg-secondary text-muted-foreground"
                    )}
                  >
                    {cfg.label}
                  </span>
                  <span className="flex items-center gap-1 font-mono">
                    <Hash className="h-3 w-3" />
                    {skill.usageCount} uses
                  </span>
                  <span className="flex items-center gap-1">
                    <Clock className="h-3 w-3" />
                    {skill.lastUsed}
                  </span>
                </div>
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
