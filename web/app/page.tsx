"use client"

import { useState } from "react"
import { AppSidebar } from "@/components/app-sidebar"
import { ProjectsView } from "@/components/projects-view"
import { TasksDashboard } from "@/components/tasks-dashboard"
import { TaskDetail } from "@/components/task-detail"
import { AgentsOverview } from "@/components/agents-overview"
import { MemoryView } from "@/components/memory-view"
import { SkillsView } from "@/components/skills-view"
import { ReposView } from "@/components/repos-view"
import { UsageView } from "@/components/usage-view"
import { SettingsView } from "@/components/settings-view"
import { NewTaskDialog } from "@/components/new-task-dialog"

export default function Home() {
  const [activeView, setActiveView] = useState("projects")
  const [selectedTask, setSelectedTask] = useState<string | null>(null)
  const [showNewTask, setShowNewTask] = useState(false)

  function handleNavigate(view: string) {
    setActiveView(view)
    setSelectedTask(null)
  }

  return (
    <div className="flex h-screen overflow-hidden bg-background">
      <AppSidebar activeView={activeView} onNavigate={handleNavigate} />

      <main className="flex flex-1 flex-col overflow-hidden">
        {/* Top header bar */}
        <header className="flex h-12 shrink-0 items-center justify-between border-b border-border px-6">
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <span className="inline-block h-1.5 w-1.5 rounded-full bg-success" />
            <span>System operational</span>
          </div>
          <div className="flex items-center gap-4 text-xs text-muted-foreground">
            <span className="font-mono">v0.4.2</span>
            <div className="flex h-7 w-7 items-center justify-center rounded-full bg-secondary text-xs font-medium text-foreground">
              A
            </div>
          </div>
        </header>

        {/* Main content */}
        <div className="flex flex-1 overflow-y-auto">
          {activeView === "projects" && (
            <ProjectsView onSelectProject={() => handleNavigate("tasks")} />
          )}
          {activeView === "tasks" && !selectedTask && (
            <TasksDashboard
              onSelectTask={(id) => setSelectedTask(id)}
              onNewTask={() => setShowNewTask(true)}
            />
          )}
          {activeView === "tasks" && selectedTask && (
            <TaskDetail
              taskId={selectedTask}
              onBack={() => setSelectedTask(null)}
            />
          )}
          {activeView === "agents" && <AgentsOverview />}
          {activeView === "memory" && <MemoryView />}
          {activeView === "skills" && <SkillsView />}
          {activeView === "repos" && <ReposView />}
          {activeView === "usage" && <UsageView />}
          {activeView === "settings" && <SettingsView />}
        </div>
      </main>

      <NewTaskDialog open={showNewTask} onClose={() => setShowNewTask(false)} />
    </div>
  )
}
