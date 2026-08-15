import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { cn } from '../../lib/utils'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../components/ui/Select'
import { settingsNavigation, settingsNavigationSections } from './navigation'

export function SettingsLayout() {
  const { pathname } = useLocation()
  const navigate = useNavigate()
  return (
    <div className="page-stack max-w-7xl">
      <div><h1 className="page-title">Settings</h1><p className="page-description">Control how Nabu works, what it may do, and the deterministic checks that keep the mission moving.</p></div>
      <div className="settings-mobile-select">
        <label className="field"><span className="field-label">Settings section</span><Select value={settingsNavigation.find((item) => item.end ? pathname === item.path : pathname.startsWith(item.path))?.path ?? '/settings'} onValueChange={(value) => navigate(value)}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent>{settingsNavigation.map((item) => <SelectItem key={item.path} value={item.path}>{item.label}</SelectItem>)}</SelectContent></Select></label>
      </div>
      <div className="settings-layout">
        <aside className="settings-sidebar"><nav aria-label="Settings navigation" className="settings-nav-groups">{settingsNavigationSections.map((section) => <section key={section.label} className="settings-nav-group" aria-labelledby={`settings-${section.label.toLowerCase()}-label`}><h2 id={`settings-${section.label.toLowerCase()}-label`} className="settings-nav-group-label">{section.label}</h2><div className="space-y-1">{section.items.map(({ label, path, icon: Icon, end }) => <NavLink key={path} to={path} end={end} className={({ isActive }) => cn('settings-nav-item', isActive && 'settings-nav-item-active')}><Icon className="size-4" />{label}</NavLink>)}</div></section>)}</nav></aside>
        <div className="min-w-0"><Outlet /></div>
      </div>
    </div>
  )
}
