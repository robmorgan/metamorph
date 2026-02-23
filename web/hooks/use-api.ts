"use client"

import { useState, useEffect, useCallback } from "react"
import { api } from "@/lib/api"
import type { Project, TaskItem, Agent, TaskItemStatus } from "@/lib/types"

// Map API task status to frontend status.
function mapTaskStatus(apiStatus: string): TaskItemStatus {
  switch (apiStatus) {
    case "open":
    case "claimed":
      return "active"
    case "done":
      return "completed"
    case "blocked":
      return "error"
    default:
      return "active"
  }
}

// Map API project status to frontend status.
function mapProjectStatus(apiStatus: string): "active" | "archived" {
  return apiStatus === "stopped" ? "archived" : "active"
}

interface UseApiResult<T> {
  data: T | null
  loading: boolean
  error: string | null
  refetch: () => void
}

function usePollingFetch<T>(
  fetcher: () => Promise<T>,
  pollInterval?: number
): UseApiResult<T> {
  const [data, setData] = useState<T | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const doFetch = useCallback(() => {
    fetcher()
      .then((result) => {
        setData(result)
        setError(null)
      })
      .catch((err) => {
        setError(err.message)
      })
      .finally(() => {
        setLoading(false)
      })
  }, [fetcher])

  useEffect(() => {
    doFetch()

    if (pollInterval && pollInterval > 0) {
      const interval = setInterval(doFetch, pollInterval)
      return () => clearInterval(interval)
    }
  }, [doFetch, pollInterval])

  return { data, loading, error, refetch: doFetch }
}

export function useProjects(): UseApiResult<Project[]> {
  const fetcher = useCallback(async () => {
    const raw = (await api.listProjects()) as Array<Record<string, unknown>>
    return raw.map(
      (p): Project => ({
        id: p.name as string,
        name: p.name as string,
        description: (p.description as string) || "",
        status: mapProjectStatus(p.status as string),
        repos: Array.isArray(p.repos)
          ? p.repos.map((r: Record<string, unknown>) => ({
              path: (r.path as string) || "",
              label: (r.label as string) || "",
            }))
          : [],
        taskCount: (p.task_count as number) || 0,
        agentCount: (p.agent_count as number) || 0,
        createdAt: (p.created_at as string) || "",
      })
    )
  }, [])

  return usePollingFetch(fetcher, 5000)
}

export function useTasks(filter?: {
  status?: string
  project?: string
}): UseApiResult<TaskItem[]> {
  const fetcher = useCallback(async () => {
    const raw = (await api.listTasks(filter)) as Array<Record<string, unknown>>
    return raw.map(
      (t): TaskItem => ({
        id: t.id as string,
        name: (t.title as string) || "",
        description: (t.description as string) || "",
        status: mapTaskStatus(t.status as string),
        projectId: (t.project as string) || "",
        repo: (t.repo as string) || undefined,
        branch: (t.branch as string) || undefined,
        priority: (t.priority as number) ?? 2,
        assignee: t.assignee as number | null,
        createdAt: (t.created_at as string) || "",
      })
    )
  }, [filter?.status, filter?.project])

  return usePollingFetch(fetcher, 5000)
}

export function useTask(id: string): UseApiResult<TaskItem> {
  const fetcher = useCallback(async () => {
    const t = (await api.getTask(id)) as Record<string, unknown>
    return {
      id: t.id as string,
      name: (t.title as string) || "",
      description: (t.description as string) || "",
      status: mapTaskStatus(t.status as string),
      projectId: (t.project as string) || "",
      repo: (t.repo as string) || undefined,
      branch: (t.branch as string) || undefined,
      priority: (t.priority as number) ?? 2,
      assignee: t.assignee as number | null,
      createdAt: (t.created_at as string) || "",
    } satisfies TaskItem
  }, [id])

  return usePollingFetch(fetcher)
}

export function useProjectState(
  projectName: string | null
): UseApiResult<Agent[]> {
  const fetcher = useCallback(async () => {
    if (!projectName) return []
    const raw = (await api.getProjectState(projectName)) as Record<
      string,
      unknown
    >
    const agents = (raw.agents as Array<Record<string, unknown>>) || []
    return agents.map(
      (a): Agent => ({
        id: String(a.id),
        role: (a.role as string) || "",
        status:
          (a.status as string) === "running"
            ? "running"
            : (a.status as string) === "exited"
              ? "error"
              : "idle",
        sessionsCompleted: (a.sessions_completed as number) || 0,
        lastActivity: (a.last_activity as string) || "",
        currentTask: (a.current_task as string) || undefined,
      })
    )
  }, [projectName])

  return usePollingFetch(fetcher, 5000)
}
