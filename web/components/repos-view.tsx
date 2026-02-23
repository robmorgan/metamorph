"use client"

import { GitBranch, ExternalLink, Star, Clock } from "lucide-react"

const repos = [
  { name: "acme/auth-service", language: "TypeScript", stars: 142, lastCommit: "2h ago", branch: "feat/jwt-refactor", linked: true },
  { name: "acme/api-gateway", language: "Go", stars: 89, lastCommit: "5h ago", branch: "main", linked: true },
  { name: "acme/admin-ui", language: "TypeScript", stars: 67, lastCommit: "1d ago", branch: "redesign/v3", linked: true },
  { name: "acme/data-pipeline", language: "Python", stars: 56, lastCommit: "3d ago", branch: "main", linked: true },
  { name: "acme/ml-api", language: "Python", stars: 34, lastCommit: "12h ago", branch: "feat/batch-inference", linked: true },
  { name: "acme/frontend", language: "TypeScript", stars: 124, lastCommit: "2d ago", branch: "main", linked: false },
  { name: "acme/infra", language: "HCL", stars: 34, lastCommit: "3d ago", branch: "main", linked: false },
]

const langColors: Record<string, string> = {
  TypeScript: "bg-info",
  Go: "bg-info",
  Python: "bg-warning",
  HCL: "bg-muted-foreground",
  Dart: "bg-info",
}

export function ReposView() {
  return (
    <div className="flex flex-1 flex-col gap-6 p-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight text-foreground">Repositories</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Connected GitHub repositories linked to your projects
        </p>
      </div>

      <div className="flex flex-col gap-2">
        <div className="grid grid-cols-[1fr_100px_80px_100px_100px] items-center gap-4 px-4 py-2 text-xs font-medium uppercase tracking-wider text-muted-foreground">
          <span>Repository</span>
          <span>Language</span>
          <span>Stars</span>
          <span>Last Commit</span>
          <span>Status</span>
        </div>
        {repos.map((repo) => (
          <div
            key={repo.name}
            className="grid grid-cols-[1fr_100px_80px_100px_100px] items-center gap-4 rounded-lg border border-border bg-card px-4 py-3"
          >
            <div className="flex items-center gap-3 overflow-hidden">
              <GitBranch className="h-4 w-4 shrink-0 text-muted-foreground" />
              <div className="flex flex-col gap-0.5 overflow-hidden">
                <span className="truncate text-sm font-medium text-foreground font-mono">{repo.name}</span>
                <span className="text-xs text-muted-foreground font-mono">{repo.branch}</span>
              </div>
            </div>
            <div className="flex items-center gap-1.5">
              <span className={`h-2.5 w-2.5 rounded-full ${langColors[repo.language] ?? "bg-muted-foreground"}`} />
              <span className="text-xs text-muted-foreground">{repo.language}</span>
            </div>
            <div className="flex items-center gap-1 text-xs text-muted-foreground">
              <Star className="h-3 w-3" />
              {repo.stars}
            </div>
            <div className="flex items-center gap-1 text-xs text-muted-foreground">
              <Clock className="h-3 w-3" />
              {repo.lastCommit}
            </div>
            <span className={`text-xs font-medium ${repo.linked ? "text-primary" : "text-muted-foreground"}`}>
              {repo.linked ? "Linked" : "Available"}
            </span>
          </div>
        ))}
      </div>
    </div>
  )
}
