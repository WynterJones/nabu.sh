import { FolderGit2 } from 'lucide-react'
import { cn } from '../lib/utils'

function initials(name: string): string {
  return name.trim().split(/\s+/).slice(0, 2).map((part) => part[0]?.toUpperCase()).join('')
}

export function WorkspaceAvatar({ name, iconUrl, className }: { name: string; iconUrl?: string; className?: string }) {
  return <span className={cn('workspace-avatar', className)} aria-hidden="true">{iconUrl ? <img src={iconUrl} alt="" className="size-full object-cover" /> : initials(name) ? <span>{initials(name)}</span> : <FolderGit2 className="size-4" />}</span>
}
