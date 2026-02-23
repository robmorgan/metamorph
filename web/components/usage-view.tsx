"use client"

import {
  mockUsageHistory,
  mockUsageByModel,
  mockUsageByProject,
} from "@/lib/mock-data"
import {
  DollarSign,
  Zap,
  TrendingUp,
  AlertTriangle,
  BarChart3,
  Clock,
} from "lucide-react"
import { Progress } from "@/components/ui/progress"
import { cn } from "@/lib/utils"
import { useState } from "react"

const MONTHLY_BUDGET = 120

export function UsageView() {
  const [period, setPeriod] = useState<"7d" | "14d" | "30d">("14d")

  const totalCost = mockUsageHistory.reduce((s, r) => s + r.cost, 0)
  const totalTokens = mockUsageHistory.reduce((s, r) => s + r.tokens, 0)
  const totalTasks = mockUsageHistory.reduce((s, r) => s + r.tasks, 0)
  const budgetPercent = Math.round((totalCost / MONTHLY_BUDGET) * 100)
  const avgDaily = totalCost / mockUsageHistory.length
  const projectedMonthly = avgDaily * 30
  const maxCost = Math.max(...mockUsageHistory.map((r) => r.cost))

  const displayData =
    period === "7d"
      ? mockUsageHistory.slice(-7)
      : period === "14d"
        ? mockUsageHistory
        : mockUsageHistory

  return (
    <div className="flex flex-1 flex-col gap-6 p-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight text-foreground">
            Usage
          </h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Token consumption, costs, and budget tracking
          </p>
        </div>
        <div className="flex gap-1 rounded-lg border border-border bg-card p-1">
          {(["7d", "14d", "30d"] as const).map((p) => (
            <button
              key={p}
              onClick={() => setPeriod(p)}
              className={cn(
                "rounded-md px-3 py-1.5 text-xs font-medium transition-colors",
                period === p
                  ? "bg-accent text-foreground"
                  : "text-muted-foreground hover:text-foreground"
              )}
            >
              {p}
            </button>
          ))}
        </div>
      </div>

      {/* Summary cards */}
      <div className="grid grid-cols-4 gap-4">
        <div className="rounded-lg border border-border bg-card p-4">
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <DollarSign className="h-3.5 w-3.5" />
            Period Cost
          </div>
          <div className="mt-2 text-2xl font-semibold text-foreground font-mono">
            ${totalCost.toFixed(2)}
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            ${avgDaily.toFixed(2)}/day avg
          </p>
        </div>
        <div className="rounded-lg border border-border bg-card p-4">
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <Zap className="h-3.5 w-3.5" />
            Tokens Used
          </div>
          <div className="mt-2 text-2xl font-semibold text-primary font-mono">
            {(totalTokens / 1_000_000).toFixed(2)}M
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            {totalTasks} tasks executed
          </p>
        </div>
        <div className="rounded-lg border border-border bg-card p-4">
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <TrendingUp className="h-3.5 w-3.5" />
            Projected Monthly
          </div>
          <div
            className={cn(
              "mt-2 text-2xl font-semibold font-mono",
              projectedMonthly > MONTHLY_BUDGET
                ? "text-warning"
                : "text-foreground"
            )}
          >
            ${projectedMonthly.toFixed(2)}
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            Budget: ${MONTHLY_BUDGET}/mo
          </p>
        </div>
        <div className="rounded-lg border border-border bg-card p-4">
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <AlertTriangle className="h-3.5 w-3.5" />
            Budget Used
          </div>
          <div className="mt-2 flex items-end gap-2">
            <span
              className={cn(
                "text-2xl font-semibold font-mono",
                budgetPercent > 80 ? "text-warning" : "text-foreground"
              )}
            >
              {budgetPercent}%
            </span>
          </div>
          <Progress
            value={Math.min(budgetPercent, 100)}
            className="mt-2 h-1.5"
          />
        </div>
      </div>

      {/* Daily cost chart (bar chart with divs) */}
      <div className="rounded-lg border border-border bg-card p-5">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2 text-sm font-medium text-foreground">
            <BarChart3 className="h-4 w-4 text-muted-foreground" />
            Daily Cost
          </div>
          <span className="text-xs text-muted-foreground font-mono">
            max ${maxCost.toFixed(2)}/day
          </span>
        </div>
        <div className="mt-4 flex items-end gap-1.5" style={{ height: 160 }}>
          {displayData.map((d, i) => {
            const height = maxCost > 0 ? (d.cost / maxCost) * 100 : 0
            return (
              <div
                key={i}
                className="group relative flex flex-1 flex-col items-center"
              >
                <div className="pointer-events-none absolute -top-8 left-1/2 -translate-x-1/2 whitespace-nowrap rounded border border-border bg-popover px-2 py-1 text-[10px] text-popover-foreground opacity-0 transition-opacity group-hover:opacity-100 z-10">
                  ${d.cost.toFixed(2)} &middot; {(d.tokens / 1000).toFixed(0)}k tokens
                </div>
                <div
                  className="w-full rounded-t bg-primary/80 transition-colors hover:bg-primary"
                  style={{ height: `${height}%`, minHeight: 2 }}
                />
              </div>
            )
          })}
        </div>
        <div className="mt-2 flex gap-1.5">
          {displayData.map((d, i) => (
            <div
              key={i}
              className="flex-1 text-center text-[10px] text-muted-foreground"
            >
              {d.date.split(" ")[1]}
            </div>
          ))}
        </div>
      </div>

      {/* Bottom two panels */}
      <div className="grid grid-cols-2 gap-4">
        {/* Cost by model */}
        <div className="rounded-lg border border-border bg-card p-5">
          <h3 className="flex items-center gap-2 text-sm font-medium text-foreground">
            <Zap className="h-4 w-4 text-muted-foreground" />
            Cost by Model
          </h3>
          <div className="mt-4 flex flex-col gap-3">
            {mockUsageByModel.map((m) => (
              <div key={m.model} className="flex flex-col gap-1.5">
                <div className="flex items-center justify-between text-xs">
                  <span className="font-mono text-foreground">{m.model}</span>
                  <span className="text-muted-foreground">
                    ${m.cost.toFixed(2)}{" "}
                    <span className="text-[10px]">({m.percentage}%)</span>
                  </span>
                </div>
                <div className="h-1.5 w-full overflow-hidden rounded-full bg-secondary">
                  <div
                    className="h-full rounded-full bg-primary/70"
                    style={{ width: `${m.percentage}%` }}
                  />
                </div>
              </div>
            ))}
          </div>
        </div>

        {/* Cost by project */}
        <div className="rounded-lg border border-border bg-card p-5">
          <h3 className="flex items-center gap-2 text-sm font-medium text-foreground">
            <Clock className="h-4 w-4 text-muted-foreground" />
            Cost by Project
          </h3>
          <div className="mt-4 flex flex-col gap-3">
            {mockUsageByProject.map((p) => {
              const pct =
                (p.cost /
                  mockUsageByProject.reduce((s, x) => s + x.cost, 0)) *
                100
              return (
                <div key={p.project} className="flex flex-col gap-1.5">
                  <div className="flex items-center justify-between text-xs">
                    <span className="text-foreground">{p.project}</span>
                    <span className="text-muted-foreground font-mono">
                      ${p.cost.toFixed(2)}{" "}
                      <span className="text-[10px]">
                        ({(p.tokens / 1_000_000).toFixed(2)}M tok)
                      </span>
                    </span>
                  </div>
                  <div className="h-1.5 w-full overflow-hidden rounded-full bg-secondary">
                    <div
                      className="h-full rounded-full bg-primary/70"
                      style={{ width: `${pct}%` }}
                    />
                  </div>
                </div>
              )
            })}
            <div className="mt-2 flex items-center justify-between border-t border-border pt-3 text-xs">
              <span className="font-medium text-foreground">Total</span>
              <span className="font-mono text-foreground">
                $
                {mockUsageByProject
                  .reduce((s, x) => s + x.cost, 0)
                  .toFixed(2)}
              </span>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
