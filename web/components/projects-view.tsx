"use client"

import { useProjects } from "@/hooks/use-api"
import { cn } from "@/lib/utils"
import {
  FolderKanban,
  Plus,
  GitBranch,
  ListChecks,
  Activity,
  ChevronRight,
  Loader2,
} from "lucide-react"
import { Button } from "@/components/ui/button"

interface ProjectsViewProps {
  onSelectProject: (projectId: string) => void
}

export function ProjectsView({ onSelectProject }: ProjectsViewProps) {
  const { data: projects, loading, error } = useProjects()

  if (loading && !projects) {
    return (
      <div className="flex flex-1 items-center justify-center p-6">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    )
  }

  if (error && !projects) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-2 p-6">
        <p className="text-sm text-destructive">Failed to load projects: {error}</p>
        <p className="text-xs text-muted-foreground">Make sure the daemon is running on port 8377</p>
      </div>
    )
  }

  const projectList = projects ?? []

  return (
    <div className="flex flex-1 flex-col gap-6 p-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight text-foreground">Projects</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Organize tasks and repositories under projects
          </p>
        </div>
        <Button className="gap-2">
          <Plus className="h-4 w-4" />
          New Project
        </Button>
      </div>

      {/* Stats row */}
      <div className="grid grid-cols-3 gap-4">
        <div className="rounded-lg border border-border bg-card p-4">
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <FolderKanban className="h-3.5 w-3.5" />
            Total Projects
          </div>
          <div className="mt-2 text-2xl font-semibold text-foreground">{projectList.length}</div>
        </div>
        <div className="rounded-lg border border-border bg-card p-4">
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <GitBranch className="h-3.5 w-3.5" />
            Linked Repos
          </div>
          <div className="mt-2 text-2xl font-semibold text-foreground">
            {projectList.reduce((sum, p) => sum + p.repos.length, 0)}
          </div>
        </div>
        <div className="rounded-lg border border-border bg-card p-4">
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <Activity className="h-3.5 w-3.5" />
            Total Tasks
          </div>
          <div className="mt-2 text-2xl font-semibold text-primary">
            {projectList.reduce((sum, p) => sum + p.taskCount, 0)}
          </div>
        </div>
      </div>

      {/* Project cards */}
      {projectList.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-lg border border-dashed border-border py-12 text-center">
          <FolderKanban className="h-8 w-8 text-muted-foreground" />
          <p className="mt-3 text-sm font-medium text-foreground">No projects yet</p>
          <p className="mt-1 text-xs text-muted-foreground">
            Register a project via the CLI to get started
          </p>
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-2 xl:grid-cols-3">
          {projectList.map((project) => (
            <button
              key={project.id}
              onClick={() => onSelectProject(project.id)}
              className="group flex flex-col gap-4 rounded-lg border border-border bg-card p-5 text-left transition-colors hover:border-primary/30 hover:bg-accent"
            >
              <div className="flex items-start justify-between">
                <div className="flex items-center gap-3">
                  <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-primary/10">
                    <FolderKanban className="h-5 w-5 text-primary" />
                  </div>
                  <div>
                    <div className="text-sm font-semibold text-foreground">{project.name}</div>
                    <div className="text-xs text-muted-foreground">{project.description}</div>
                  </div>
                </div>
                <ChevronRight className="h-4 w-4 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100" />
              </div>

              {/* Repos */}
              <div className="flex flex-col gap-1.5">
                {project.repos.map((repo) => (
                  <div key={repo.path} className="flex items-center gap-2 text-xs text-muted-foreground">
                    <GitBranch className="h-3 w-3 shrink-0" />
                    <span className="truncate font-mono">{repo.label || repo.path}</span>
                  </div>
                ))}
              </div>

              {/* Footer stats */}
              <div className="flex items-center gap-4 border-t border-border pt-3 text-xs text-muted-foreground">
                <div className="flex items-center gap-1.5">
                  <ListChecks className="h-3 w-3" />
                  {project.taskCount} tasks
                </div>
                <span className={cn(
                  "ml-auto rounded-full px-2 py-0.5 text-[10px] font-medium",
                  project.status === "active"
                    ? "bg-success/10 text-success"
                    : "bg-muted text-muted-foreground"
                )}>
                  {project.status}
                </span>
              </div>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
