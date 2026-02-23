"use client"

import { mockMemories } from "@/lib/mock-data"
import { Brain, Search, Hash, Clock, Eye, Tag } from "lucide-react"
import { Input } from "@/components/ui/input"
import { Button } from "@/components/ui/button"
import { useState } from "react"

export function MemoryView() {
  const [search, setSearch] = useState("")
  const [selectedTag, setSelectedTag] = useState<string | null>(null)

  const allTags = Array.from(new Set(mockMemories.flatMap((m) => m.tags)))

  const filtered = mockMemories.filter((m) => {
    const matchesSearch =
      m.content.toLowerCase().includes(search.toLowerCase()) ||
      m.source.toLowerCase().includes(search.toLowerCase())
    const matchesTag = !selectedTag || m.tags.includes(selectedTag)
    return matchesSearch && matchesTag
  })

  return (
    <div className="flex flex-1 flex-col gap-6 p-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight text-foreground">Memory</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Knowledge base agents use to maintain context across tasks
          </p>
        </div>
        <Button className="gap-2">
          <Brain className="h-4 w-4" />
          Add Memory
        </Button>
      </div>

      {/* Stats */}
      <div className="grid grid-cols-3 gap-4">
        <div className="rounded-lg border border-border bg-card p-4">
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <Brain className="h-3.5 w-3.5" />
            Total Memories
          </div>
          <div className="mt-2 text-2xl font-semibold text-foreground">{mockMemories.length}</div>
        </div>
        <div className="rounded-lg border border-border bg-card p-4">
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <Tag className="h-3.5 w-3.5" />
            Unique Tags
          </div>
          <div className="mt-2 text-2xl font-semibold text-foreground">{allTags.length}</div>
        </div>
        <div className="rounded-lg border border-border bg-card p-4">
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <Eye className="h-3.5 w-3.5" />
            Total Accesses
          </div>
          <div className="mt-2 text-2xl font-semibold text-primary font-mono">
            {mockMemories.reduce((sum, m) => sum + m.accessCount, 0)}
          </div>
        </div>
      </div>

      {/* Search + tag filter */}
      <div className="flex items-center gap-3">
        <div className="relative flex-1">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            placeholder="Search memories..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="pl-9"
          />
        </div>
        <div className="flex flex-wrap gap-1.5">
          <button
            onClick={() => setSelectedTag(null)}
            className={`rounded-md px-2.5 py-1 text-xs font-medium transition-colors ${
              !selectedTag ? "bg-primary/10 text-primary" : "bg-secondary text-muted-foreground hover:text-foreground"
            }`}
          >
            All
          </button>
          {allTags.map((tag) => (
            <button
              key={tag}
              onClick={() => setSelectedTag(selectedTag === tag ? null : tag)}
              className={`rounded-md px-2.5 py-1 text-xs font-medium transition-colors ${
                selectedTag === tag
                  ? "bg-primary/10 text-primary"
                  : "bg-secondary text-muted-foreground hover:text-foreground"
              }`}
            >
              {tag}
            </button>
          ))}
        </div>
      </div>

      {/* Memory cards */}
      <div className="flex flex-col gap-3">
        {filtered.map((memory) => (
          <div
            key={memory.id}
            className="rounded-lg border border-border bg-card p-4 transition-colors hover:border-primary/20"
          >
            <div className="flex items-start gap-3">
              <div className="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-primary/10">
                <Brain className="h-4 w-4 text-primary" />
              </div>
              <div className="flex flex-1 flex-col gap-2">
                <p className="text-sm leading-relaxed text-foreground">{memory.content}</p>
                <div className="flex items-center gap-4 text-xs text-muted-foreground">
                  <span className="flex items-center gap-1">
                    <Hash className="h-3 w-3" />
                    <span className="font-mono">{memory.source}</span>
                  </span>
                  <span className="flex items-center gap-1">
                    <Clock className="h-3 w-3" />
                    {memory.createdAt}
                  </span>
                  <span className="flex items-center gap-1">
                    <Eye className="h-3 w-3" />
                    {memory.accessCount} accesses
                  </span>
                </div>
                <div className="flex gap-1.5">
                  {memory.tags.map((tag) => (
                    <span
                      key={tag}
                      className="rounded bg-secondary px-2 py-0.5 text-[10px] font-medium text-muted-foreground"
                    >
                      {tag}
                    </span>
                  ))}
                </div>
              </div>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
