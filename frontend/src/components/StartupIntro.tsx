import { useEffect, useRef, useState } from 'react'
import { cn } from '../lib/utils'

const sessionKey = 'nabu-intro-seen'

function shouldPlayIntro(): boolean {
  try {
    if (window.sessionStorage.getItem(sessionKey)) return false
    window.sessionStorage.setItem(sessionKey, '1')
    return true
  } catch {
    return true
  }
}

const introShouldPlay = shouldPlayIntro()

export function StartupIntro({ version, children }: { version?: string; children: React.ReactNode }) {
  const [playing, setPlaying] = useState(introShouldPlay)
  const [leaving, setLeaving] = useState(false)
  const appRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (playing) appRef.current?.setAttribute('inert', '')
    else appRef.current?.removeAttribute('inert')
  }, [playing])

  useEffect(() => {
    if (!playing) return
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
      const reducedTimer = window.setTimeout(() => setPlaying(false), 450)
      return () => window.clearTimeout(reducedTimer)
    }
    const leaveTimer = window.setTimeout(() => setLeaving(true), 2350)
    const doneTimer = window.setTimeout(() => setPlaying(false), 2850)
    return () => {
      window.clearTimeout(leaveTimer)
      window.clearTimeout(doneTimer)
    }
  }, [playing])

  return (
    <div className={cn('startup-stage', playing && 'startup-stage-active')}>
      <div ref={appRef} className={cn('startup-app', playing && 'startup-app-waiting')} aria-hidden={playing || undefined}>{children}</div>
      {playing ? (
        <div className={cn('startup-intro', leaving && 'startup-intro-leaving')} role="status" aria-label={`Nabu ${version ? `version ${version}` : ''} is starting`}>
          <img className="startup-background" src="/assets/owlbg.webp" alt="" width="1672" height="941" fetchPriority="high" />
          <div className="startup-shade" />
          <div className="startup-brand-stack">
            <img className="startup-nabu-logo" src="/assets/nabu-splash-transparent.webp" alt="Nabu.sh" width="821" height="592" fetchPriority="high" />
            <div className="startup-maker">
              <span>Made by</span>
              <img src="/assets/wynter-logo.webp" alt="Wynter.ai" width="510" height="250" />
            </div>
            <span className="startup-version">Version {version || '0.1.0'}</span>
          </div>
        </div>
      ) : null}
    </div>
  )
}
