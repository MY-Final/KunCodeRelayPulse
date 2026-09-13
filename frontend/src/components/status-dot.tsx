import { cn } from "@/lib/utils"
import type { StatusCode } from "@/types"

const statusStyles: Record<string, string> = {
  unknown: "bg-muted-foreground/45",
  up: "bg-success",
  slow: "bg-warning",
  down: "bg-destructive",
}

export function StatusDot({ status, className }: { status?: StatusCode; className?: string }) {
  const key = status === 1 ? "up" : status === 2 ? "slow" : status === 0 ? "down" : "unknown"
  return <span aria-hidden="true" className={cn("inline-block h-2.5 w-2.5 shrink-0 rounded-full", statusStyles[key], className)} />
}
