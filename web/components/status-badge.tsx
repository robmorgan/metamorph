import { cn } from "@/lib/utils"

type StatusType = "active" | "paused" | "completed" | "error" | "pending" | "in-progress" | "failed" | "blocked" | "idle" | "running"

const statusConfig: Record<StatusType, { label: string; dotColor: string; bgColor: string; textColor: string }> = {
  active: { label: "Active", dotColor: "bg-success", bgColor: "bg-success/10", textColor: "text-success" },
  running: { label: "Running", dotColor: "bg-success", bgColor: "bg-success/10", textColor: "text-success" },
  "in-progress": { label: "In Progress", dotColor: "bg-info", bgColor: "bg-info/10", textColor: "text-info" },
  completed: { label: "Completed", dotColor: "bg-muted-foreground", bgColor: "bg-muted", textColor: "text-muted-foreground" },
  idle: { label: "Idle", dotColor: "bg-muted-foreground", bgColor: "bg-muted", textColor: "text-muted-foreground" },
  paused: { label: "Paused", dotColor: "bg-warning", bgColor: "bg-warning/10", textColor: "text-warning" },
  pending: { label: "Pending", dotColor: "bg-muted-foreground", bgColor: "bg-muted", textColor: "text-muted-foreground" },
  blocked: { label: "Blocked", dotColor: "bg-warning", bgColor: "bg-warning/10", textColor: "text-warning" },
  error: { label: "Error", dotColor: "bg-destructive", bgColor: "bg-destructive/10", textColor: "text-destructive" },
  failed: { label: "Failed", dotColor: "bg-destructive", bgColor: "bg-destructive/10", textColor: "text-destructive" },
}

export function StatusBadge({ status }: { status: StatusType }) {
  const config = statusConfig[status]
  return (
    <span className={cn("inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium", config.bgColor, config.textColor)}>
      <span className={cn("h-1.5 w-1.5 rounded-full", config.dotColor, (status === "running" || status === "in-progress" || status === "active") && "animate-pulse")} />
      {config.label}
    </span>
  )
}
