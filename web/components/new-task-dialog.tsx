"use client"

import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { FolderPlus, ArrowRight, Loader2 } from "lucide-react"
import { useState } from "react"
import { useProjects } from "@/hooks/use-api"
import { api } from "@/lib/api"

interface NewTaskDialogProps {
  open: boolean
  onClose: () => void
}

export function NewTaskDialog({ open, onClose }: NewTaskDialogProps) {
  const { data: projects } = useProjects()
  const projectList = projects ?? []

  const [selectedProject, setSelectedProject] = useState<string>("")
  const [title, setTitle] = useState("")
  const [description, setDescription] = useState("")
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Auto-select first project when projects load.
  const effectiveProject = selectedProject || (projectList.length > 0 ? projectList[0].name : "")

  function handleClose() {
    setTitle("")
    setDescription("")
    setError(null)
    onClose()
  }

  async function handleSubmit() {
    if (!title.trim()) {
      setError("Task name is required")
      return
    }

    setSubmitting(true)
    setError(null)

    try {
      await api.createTask({
        title: title.trim(),
        description: description.trim(),
        project: effectiveProject || undefined,
      })
      handleClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to create task")
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && handleClose()}>
      <DialogContent className="max-w-lg bg-card border-border">
        <DialogHeader>
          <DialogTitle className="text-foreground">New Task</DialogTitle>
          <DialogDescription>
            Describe the task objective. Agents will decompose it into discrete, executable subtasks.
          </DialogDescription>
        </DialogHeader>
        <div className="mt-4 flex flex-col gap-4">
          {projectList.length > 0 && (
            <div className="flex flex-col gap-2">
              <Label htmlFor="project" className="text-foreground">Project</Label>
              <select
                id="project"
                value={effectiveProject}
                onChange={(e) => setSelectedProject(e.target.value)}
                className="flex h-9 w-full rounded-md border border-input bg-input px-3 py-1 text-sm text-foreground shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              >
                {projectList.map((p) => (
                  <option key={p.name} value={p.name}>{p.name}</option>
                ))}
              </select>
            </div>
          )}
          <div className="flex flex-col gap-2">
            <Label htmlFor="name" className="text-foreground">Task Name</Label>
            <Input
              id="name"
              placeholder="auth-service-refactor"
              className="font-mono"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
            />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="description" className="text-foreground">Description</Label>
            <Textarea
              id="description"
              placeholder="Describe the high-level objective for this task. Agents will break it into a sequence of subtasks..."
              rows={4}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
            />
          </div>
          {error && (
            <p className="text-sm text-destructive">{error}</p>
          )}
          <div className="flex justify-end gap-2 pt-2">
            <Button variant="outline" onClick={handleClose}>
              Cancel
            </Button>
            <Button onClick={handleSubmit} disabled={submitting} className="gap-2">
              {submitting ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              ) : (
                <FolderPlus className="h-3.5 w-3.5" />
              )}
              Create Task
              <ArrowRight className="h-3.5 w-3.5" />
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
