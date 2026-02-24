const API_BASE = process.env.NEXT_PUBLIC_API_URL || "/api"

export interface CreateTaskRequest {
  id?: string
  title: string
  description?: string
  priority?: number
  role_affinity?: string
  project?: string
  repo?: string
  branch?: string
  depends_on?: string[]
}

async function fetchJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, init)
  if (!res.ok) {
    const body = await res.json().catch(() => ({}))
    throw new Error(body.error || `HTTP ${res.status}`)
  }
  return res.json()
}

export const api = {
  // Projects
  listProjects: () => fetchJSON<unknown[]>(`${API_BASE}/projects`),
  getProject: (name: string) => fetchJSON<unknown>(`${API_BASE}/projects/${name}`),
  getProjectState: (name: string) =>
    fetchJSON<unknown>(`${API_BASE}/projects/${name}/state`),

  // Tasks
  listTasks: (params?: { status?: string; project?: string }) => {
    const query = new URLSearchParams()
    if (params?.status) query.set("status", params.status)
    if (params?.project) query.set("project", params.project)
    const qs = query.toString()
    return fetchJSON<unknown[]>(`${API_BASE}/tasks${qs ? `?${qs}` : ""}`)
  },
  getTask: (id: string) => fetchJSON<unknown>(`${API_BASE}/tasks/${id}`),
  createTask: (data: CreateTaskRequest) =>
    fetchJSON<unknown>(`${API_BASE}/tasks`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    }),
  updateTask: (id: string, data: Record<string, unknown>) =>
    fetchJSON<unknown>(`${API_BASE}/tasks/${id}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    }),

  // Health
  health: () => fetchJSON<{ status: string }>(`${API_BASE}/health`),
}
