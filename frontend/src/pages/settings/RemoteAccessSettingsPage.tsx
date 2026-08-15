import { Check, Clipboard, ExternalLink, LoaderCircle, LockKeyhole, RefreshCw, ShieldAlert, Unplug } from 'lucide-react'
import { useEffect, useState } from 'react'
import { ConfirmDialog } from '../../components/ConfirmDialog'
import { InlineError, PageLoading } from '../../components/PageState'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { Card, SectionHeader } from '../../components/ui/Card'
import { remoteAccessApi } from '../../features/remote-access/api'
import { useResource } from '../../hooks/useResource'

export function RemoteAccessSettingsPage() {
  const { data, setData, loading, error, refresh } = useResource(remoteAccessApi.tailscale)
  const [action, setAction] = useState<'enable' | 'disable' | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const [authorizationUrl, setAuthorizationUrl] = useState<string | null>(null)
  const [confirmDisable, setConfirmDisable] = useState(false)

  useEffect(() => {
    if (!authorizationUrl || data?.serveConfigured) return
    let checking = false
    const poll = window.setInterval(async () => {
      if (checking) return
      checking = true
      try {
        const result = await remoteAccessApi.enableTailscale()
        setData(result.status)
        if (result.status.serveConfigured) setAuthorizationUrl(null)
      } catch {
        // Keep the explicit retry available; transient CLI errors are expected while consent is pending.
      } finally {
        checking = false
      }
    }, 5000)
    return () => window.clearInterval(poll)
  }, [authorizationUrl, data?.serveConfigured, setData])

  useEffect(() => {
    if (data?.serveConfigured) setAuthorizationUrl(null)
  }, [data?.serveConfigured])

  const enable = async () => {
    setAction('enable')
    setActionError(null)
    try {
      const result = await remoteAccessApi.enableTailscale()
      setData(result.status)
      if (result.authorizationUrl) {
        setAuthorizationUrl(result.authorizationUrl)
      }
    } catch (caught) {
      setActionError(caught instanceof Error ? caught.message : 'Private access could not be enabled.')
    } finally {
      setAction(null)
    }
  }

  const finishAuthorization = async () => {
    await enable()
    await refresh()
  }

  const disable = async () => {
    setAction('disable')
    setActionError(null)
    try {
      setData(await remoteAccessApi.disableTailscale())
      setAuthorizationUrl(null)
      setConfirmDisable(false)
    } catch (caught) {
      setActionError(caught instanceof Error ? caught.message : 'Private access could not be removed.')
    } finally {
      setAction(null)
    }
  }

  if (loading && !data) return <PageLoading label="Checking Tailscale…" />
  return (
    <div className="settings-content-stack">
      <div><p className="eyebrow">Remote access</p><h2 className="settings-title">Open Nabu from your devices</h2><p className="settings-description">Use Tailscale Serve for encrypted, private access from your phone or another computer without opening Nabu to the internet.</p></div>
      {error ? <InlineError message={error} /> : null}
      <Card className="p-5 shadow-none">
        <SectionHeader eyebrow="Tailscale" title={data?.connected ? 'Connected to your tailnet' : data?.installed ? 'Installed but not connected' : 'Not installed'} action={<Button variant="ghost" size="icon" onClick={() => void refresh()} aria-label="Refresh Tailscale status"><RefreshCw className="size-4" /></Button>} />
        <div className="mt-4 flex flex-wrap items-center gap-2">
          <Badge variant={data?.connected ? 'success' : 'warning'}>{data?.connected ? 'Connected' : 'Setup needed'}</Badge>
          {data?.serveConfigured ? <Badge variant="success">Private Serve active</Badge> : <Badge>Serve not configured</Badge>}
          {data?.funnelConfigured ? <Badge variant="danger">Public Funnel active</Badge> : null}
          {data?.version ? <span className="text-xs text-muted">Tailscale {data.version.split('-')[0]}</span> : null}
        </div>
        {data?.privateUrl && data.serveConfigured ? <div className="mt-4 rounded-xl border border-accent/20 bg-accent/[0.045] p-4"><p className="eyebrow text-accent">Your private Nabu address</p><div className="mt-2 flex min-w-0 flex-wrap items-center gap-2"><code className="min-w-0 flex-1 break-all text-sm text-ink">{data.privateUrl}</code><CopyButton value={data.privateUrl} label="Copy address" /></div><p className="mt-2 text-xs leading-relaxed text-muted">Only devices signed into your tailnet and allowed by its access policy can open this address.</p></div> : null}
      </Card>

      <Card className="p-5 shadow-none">
        <SectionHeader eyebrow="Private setup" title={data?.serveConfigured ? 'Private access is ready' : authorizationUrl ? 'Approve Tailscale Serve' : 'Connect this Nabu'} />
        {!data?.installed ? <><p className="mt-2 text-sm leading-relaxed text-muted">Install Tailscale, sign in, then return here to connect Nabu.</p><Button asChild variant="primary" className="mt-4"><a href="https://tailscale.com/download" target="_blank" rel="noreferrer">Download Tailscale<ExternalLink className="size-4" /></a></Button></> : !data.connected ? <p className="mt-2 text-sm leading-relaxed text-muted">Open Tailscale and sign in to a tailnet. Nabu will detect it when you refresh.</p> : data.serveConfigured ? <div className="mt-4 flex flex-wrap items-center gap-2"><Button asChild variant="primary"><a href={data.privateUrl} target="_blank" rel="noreferrer">Open private Nabu<ExternalLink className="size-4" /></a></Button><Button variant="secondary" onClick={() => setConfirmDisable(true)}><Unplug className="size-4" />Disconnect</Button></div> : authorizationUrl ? <div className="mt-4 rounded-xl border border-warning/25 bg-warning/[0.055] p-4"><div className="flex items-start gap-3"><span className="flex size-10 shrink-0 items-center justify-center rounded-lg border border-warning/25 bg-warning/10 text-warning"><LockKeyhole className="size-5" /></span><div><h3 className="text-sm font-semibold text-ink">One approval is required</h3><p className="mt-1 text-xs leading-relaxed text-muted">Tailscale requires a tailnet administrator to enable private HTTPS certificates. Approve it in the Tailscale tab, then return here.</p></div></div><div className="mt-4 flex flex-wrap gap-2"><Button asChild variant="primary"><a href={authorizationUrl} target="_blank" rel="noreferrer">Authorize in Tailscale<ExternalLink className="size-4" /></a></Button><Button variant="secondary" onClick={() => void finishAuthorization()} disabled={action === 'enable'}>{action === 'enable' ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <RefreshCw className="size-4" />}{action === 'enable' ? 'Checking…' : 'I approved it'}</Button></div></div> : <><p className="mt-2 max-w-2xl text-sm leading-relaxed text-muted">Nabu will ask Tailscale to create a private HTTPS address that only devices allowed on your tailnet can reach. Public Funnel access stays off.</p><Button variant="primary" className="mt-4" onClick={() => void enable()} disabled={action === 'enable'}>{action === 'enable' ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" /> : <LockKeyhole className="size-4" />}{action === 'enable' ? 'Connecting…' : 'Enable private access'}</Button></>}
        {actionError ? <div className="mt-4"><InlineError message={actionError} /></div> : null}
      </Card>

      <Card className="border-warning/25 p-5 shadow-none">
        <div className="flex items-start gap-3"><span className="flex size-10 shrink-0 items-center justify-center rounded-lg border border-warning/25 bg-warning/10 text-warning"><ShieldAlert className="size-5" /></span><div className="min-w-0"><h3 className="text-sm font-semibold text-ink">Keep Funnel off for now</h3><p className="mt-1.5 text-pretty text-xs leading-relaxed text-muted">Tailscale Funnel makes Nabu public on the internet. Nabu currently trusts a private tailnet boundary and does not provide a separate internet-facing login, so Serve is the safe option.</p>{data?.funnelConfigured ? <p className="mt-2 text-xs font-medium text-warning">Funnel appears to be active. Review and disable it before storing sensitive business context.</p> : null}</div></div>
      </Card>

      <ConfirmDialog open={confirmDisable} onOpenChange={setConfirmDisable} title="Disconnect private access?" description="This removes Nabu's Tailscale Serve route. Local access at 127.0.0.1 remains available." confirmLabel="Disconnect" destructive pending={action === 'disable'} onConfirm={() => void disable()} />
    </div>
  )
}

function CopyButton({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    await navigator.clipboard.writeText(value)
    setCopied(true)
    window.setTimeout(() => setCopied(false), 1600)
  }
  return <Button variant="ghost" size="icon" onClick={() => void copy()} aria-label={label}>{copied ? <Check className="size-4 text-accent" /> : <Clipboard className="size-4" />}</Button>
}
