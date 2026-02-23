"use client"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { Separator } from "@/components/ui/separator"

export function SettingsView() {
  return (
    <div className="flex flex-1 flex-col gap-6 p-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight text-foreground">Settings</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Configure your MetaMorph workspace and agent defaults
        </p>
      </div>

      <div className="max-w-2xl">
        {/* General */}
        <section className="flex flex-col gap-4">
          <h2 className="text-sm font-semibold text-foreground">General</h2>
          <div className="flex flex-col gap-3 rounded-lg border border-border bg-card p-4">
            <div className="flex flex-col gap-2">
              <Label htmlFor="workspace" className="text-foreground">Workspace Name</Label>
              <Input id="workspace" defaultValue="acme-team" className="font-mono" />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="api-key" className="text-foreground">API Key</Label>
              <Input id="api-key" defaultValue="mm_sk_...a8f3" type="password" className="font-mono" />
            </div>
          </div>
        </section>

        <Separator className="my-6" />

        {/* Agent defaults */}
        <section className="flex flex-col gap-4">
          <h2 className="text-sm font-semibold text-foreground">Agent Defaults</h2>
          <div className="flex flex-col gap-4 rounded-lg border border-border bg-card p-4">
            <div className="flex items-center justify-between">
              <div>
                <div className="text-sm font-medium text-foreground">Auto-approve plans</div>
                <div className="text-xs text-muted-foreground">
                  Automatically approve generated execution plans without review
                </div>
              </div>
              <Switch />
            </div>
            <Separator />
            <div className="flex items-center justify-between">
              <div>
                <div className="text-sm font-medium text-foreground">Deterministic mode</div>
                <div className="text-xs text-muted-foreground">
                  Use temperature 0 and fixed seeds for reproducible outputs
                </div>
              </div>
              <Switch defaultChecked />
            </div>
            <Separator />
            <div className="flex items-center justify-between">
              <div>
                <div className="text-sm font-medium text-foreground">Auto-retry on failure</div>
                <div className="text-xs text-muted-foreground">
                  Retry failed tasks up to 3 times before marking as failed
                </div>
              </div>
              <Switch defaultChecked />
            </div>
            <Separator />
            <div className="flex flex-col gap-2">
              <Label htmlFor="max-tokens" className="text-foreground">Max Tokens per Step</Label>
              <Input id="max-tokens" defaultValue="4096" type="number" className="font-mono w-32" />
            </div>
          </div>
        </section>

        <Separator className="my-6" />

        {/* Danger zone */}
        <section className="flex flex-col gap-4">
          <h2 className="text-sm font-semibold text-destructive">Danger Zone</h2>
          <div className="flex items-center justify-between rounded-lg border border-destructive/30 bg-destructive/5 p-4">
            <div>
              <div className="text-sm font-medium text-foreground">Delete Workspace</div>
              <div className="text-xs text-muted-foreground">
                Permanently delete this workspace and all associated data
              </div>
            </div>
            <Button variant="destructive" size="sm">Delete</Button>
          </div>
        </section>
      </div>
    </div>
  )
}
