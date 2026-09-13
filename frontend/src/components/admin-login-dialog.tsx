import { useState, type FormEvent } from "react"
import { KeyRound, LoaderCircle } from "lucide-react"
import { login } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"

interface AdminLoginDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSuccess: () => Promise<void>
}

export function AdminLoginDialog({ open, onOpenChange, onSuccess }: AdminLoginDialogProps) {
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [error, setError] = useState("")
  const [pending, setPending] = useState(false)

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setPending(true)
    setError("")
    try {
      await login(username, password)
      setPassword("")
      onOpenChange(false)
      await onSuccess()
    } catch {
      setError("用户名或密码错误")
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2"><KeyRound className="h-4 w-4 text-primary" />管理员登录</DialogTitle>
          <DialogDescription>登录后查看隐藏渠道并管理探测配置。</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-4">
          <div className="space-y-2"><Label htmlFor="admin-username">用户名</Label><Input id="admin-username" autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} required /></div>
          <div className="space-y-2"><Label htmlFor="admin-password">密码</Label><Input id="admin-password" type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} required /></div>
          {error ? <p role="alert" className="text-sm text-destructive">{error}</p> : null}
          <DialogFooter><Button type="submit" disabled={pending}>{pending ? <LoaderCircle className="h-4 w-4 animate-spin" /> : <KeyRound className="h-4 w-4" />}{pending ? "验证中" : "登录"}</Button></DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
