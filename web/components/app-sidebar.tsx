"use client"

import { cn } from "@/lib/utils"
import {
  FolderKanban,
  ListChecks,
  Activity,
  Brain,
  Blocks,
  GitBranch,
  Gauge,
  Settings,
  ChevronLeft,
  ChevronRight,
} from "lucide-react"
import Image from "next/image"
import { useState } from "react"

interface AppSidebarProps {
  activeView: string
  onNavigate: (view: string) => void
}

const navItems = [
  { id: "projects", label: "Projects", icon: FolderKanban },
  { id: "tasks", label: "Tasks", icon: ListChecks },
  { id: "agents", label: "Agents", icon: Activity },
  { id: "memory", label: "Memory", icon: Brain },
  { id: "skills", label: "Skills", icon: Blocks },
  { id: "repos", label: "Repositories", icon: GitBranch },
  { id: "usage", label: "Usage", icon: Gauge },
  { id: "settings", label: "Settings", icon: Settings },
]

export function AppSidebar({ activeView, onNavigate }: AppSidebarProps) {
  const [collapsed, setCollapsed] = useState(false)

  return (
    <aside
      className={cn(
        "flex h-screen flex-col border-r border-border bg-sidebar transition-all duration-200",
        collapsed ? "w-16" : "w-60"
      )}
    >
      <div className="flex items-center gap-2 border-b border-border px-4 py-4">
        <Image
          src="/images/metamorph-logo.png"
          alt="MetaMorph"
          width={32}
          height={32}
          className="h-8 w-8 shrink-0 rounded-lg"
        />
        {!collapsed && (
          <span className="text-sm font-semibold tracking-tight text-sidebar-foreground">
            MetaMorph
          </span>
        )}
      </div>

      <nav className="flex flex-1 flex-col gap-1 p-2">
        {navItems.map((item) => {
          const isActive = activeView === item.id
          return (
            <button
              key={item.id}
              onClick={() => onNavigate(item.id)}
              className={cn(
                "flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors",
                isActive
                  ? "bg-sidebar-accent text-sidebar-primary"
                  : "text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-foreground"
              )}
            >
              <item.icon className="h-4 w-4 shrink-0" />
              {!collapsed && <span>{item.label}</span>}
            </button>
          )
        })}
      </nav>

      <div className="border-t border-border p-2">
        <button
          onClick={() => setCollapsed(!collapsed)}
          className="flex w-full items-center justify-center rounded-md p-2 text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-foreground"
        >
          {collapsed ? (
            <ChevronRight className="h-4 w-4" />
          ) : (
            <ChevronLeft className="h-4 w-4" />
          )}
        </button>
      </div>
    </aside>
  )
}
