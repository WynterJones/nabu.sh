import { AlertTriangle, RefreshCw, WifiOff } from 'lucide-react'
import { Button } from './ui/Button'
import { EmptyState } from './ui/Card'

export function PageLoading({ label = 'Loading Nabu…' }: { label?: string }) {
  return (
    <div className="flex min-h-64 items-center justify-center" role="status">
      <div className="flex items-center gap-3 text-sm text-muted">
        <RefreshCw className="size-4 animate-spin motion-reduce:animate-none" />
        <span>{label}</span>
      </div>
    </div>
  )
}

export function ConnectionError({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <EmptyState
      icon={<WifiOff className="size-5" />}
      title="Nabu is not responding"
      description={message}
      action={<Button variant="primary" onClick={onRetry}><RefreshCw className="size-4" />Try again</Button>}
    />
  )
}

export function InlineError({ message }: { message: string }) {
  return <div className="flex items-start gap-2 rounded-lg border border-danger/30 bg-danger/10 px-3 py-2.5 text-sm text-danger" role="alert"><AlertTriangle className="mt-0.5 size-4 shrink-0" /><span>{message}</span></div>
}
