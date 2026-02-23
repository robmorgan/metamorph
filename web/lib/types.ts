export type ProjectStatus = "active" | "archived"
export type TaskItemStatus = "active" | "paused" | "completed" | "error"
export type SubtaskStatus = "pending" | "in-progress" | "completed" | "failed" | "blocked"
export type AgentStatus = "idle" | "running" | "paused" | "error"

export interface Project {
  id: string
  name: string
  description: string
  status: ProjectStatus
  repos: ProjectRepo[]
  taskCount: number
  agentCount: number
  createdAt: string
}

export interface ProjectRepo {
  path: string
  label: string
}

export interface TaskItem {
  id: string
  name: string
  description: string
  status: TaskItemStatus
  projectId: string
  repo?: string
  branch?: string
  priority: number
  assignee: number | null
  createdAt: string
}

export interface Subtask {
  id: string
  title: string
  description: string
  status: SubtaskStatus
  steps: SubtaskStep[]
  agentId?: string
  duration?: string
  createdAt: string
}

export interface SubtaskStep {
  id: string
  title: string
  status: SubtaskStatus
  duration?: string
}

export interface Agent {
  id: string
  role: string
  status: AgentStatus
  sessionsCompleted: number
  lastActivity: string
  currentTask?: string
}

export interface AgentLog {
  id: string
  timestamp: string
  level: "info" | "warn" | "error" | "debug"
  message: string
}

export interface Memory {
  id: string
  content: string
  source: string
  tags: string[]
  createdAt: string
  accessCount: number
}

export interface Skill {
  id: string
  name: string
  description: string
  category: "code" | "test" | "deploy" | "review" | "document"
  usageCount: number
  lastUsed: string
  enabled: boolean
}

export interface UsageRecord {
  date: string
  tokens: number
  cost: number
  tasks: number
}

export interface UsageByModel {
  model: string
  tokens: number
  cost: number
  percentage: number
}

export interface UsageByProject {
  project: string
  tokens: number
  cost: number
}
