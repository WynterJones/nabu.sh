import { Navigate, Route, Routes } from 'react-router-dom'
import { lazy, Suspense } from 'react'
import { AppShell } from './components/AppShell'
import { ConnectionError, PageLoading } from './components/PageState'
import { SetupPage } from './pages/SetupPage'
import { useNabu } from './state/NabuContext'
import { StartupIntro } from './components/StartupIntro'
import { ContextSetupGate } from './components/ContextSetupGate'

const TasksPage = lazy(() => import('./pages/TasksPage').then((module) => ({ default: module.TasksPage })))
const CalendarPage = lazy(() => import('./pages/CalendarPage').then((module) => ({ default: module.CalendarPage })))
const DatabasePage = lazy(() => import('./pages/DatabasePage').then((module) => ({ default: module.DatabasePage })))
const DatabaseDatasetPage = lazy(() => import('./pages/DatabasePage').then((module) => ({ default: module.DatabaseDatasetPage })))
const OutputsPage = lazy(() => import('./pages/OutputsPage').then((module) => ({ default: module.OutputsPage })))
const AppsPage = lazy(() => import('./pages/AppsPage').then((module) => ({ default: module.AppsPage })))
const TaskDetailPage = lazy(() => import('./pages/TaskDetailPage').then((module) => ({ default: module.TaskDetailPage })))
const RunDetailPage = lazy(() => import('./pages/RunDetailPage').then((module) => ({ default: module.RunDetailPage })))
const ChatPage = lazy(() => import('./pages/ChatPage').then((module) => ({ default: module.ChatPage })))
const ReportsPage = lazy(() => import('./pages/ReportsPage').then((module) => ({ default: module.ReportsPage })))
const ReportDetailPage = lazy(() => import('./pages/ReportsPage').then((module) => ({ default: module.ReportDetailPage })))
const ApprovalDetailPage = lazy(() => import('./pages/ApprovalDetailPage').then((module) => ({ default: module.ApprovalDetailPage })))
const SettingsLayout = lazy(() => import('./pages/settings/SettingsLayout').then((module) => ({ default: module.SettingsLayout })))
const OperatorSettingsPage = lazy(() => import('./pages/settings/OperatorSettingsPage').then((module) => ({ default: module.OperatorSettingsPage })))
const WorkspacesSettingsPage = lazy(() => import('./pages/settings/WorkspacesSettingsPage').then((module) => ({ default: module.WorkspacesSettingsPage })))
const PolicySettingsPage = lazy(() => import('./pages/settings/PolicySettingsPage').then((module) => ({ default: module.PolicySettingsPage })))
const SchedulesSettingsPage = lazy(() => import('./pages/settings/SchedulesSettingsPage').then((module) => ({ default: module.SchedulesSettingsPage })))
const ScriptsSettingsPage = lazy(() => import('./pages/settings/ScriptsSettingsPage').then((module) => ({ default: module.ScriptsSettingsPage })))
const MemorySettingsPage = lazy(() => import('./pages/settings/MemorySettingsPage').then((module) => ({ default: module.MemorySettingsPage })))
const SoulSettingsPage = lazy(() => import('./pages/settings/SoulSettingsPage').then((module) => ({ default: module.SoulSettingsPage })))
const IntegrationsSettingsPage = lazy(() => import('./pages/settings/IntegrationsSettingsPage').then((module) => ({ default: module.IntegrationsSettingsPage })))
const MCPSettingsPage = lazy(() => import('./pages/settings/MCPSettingsPage').then((module) => ({ default: module.MCPSettingsPage })))
const RemoteAccessSettingsPage = lazy(() => import('./pages/settings/RemoteAccessSettingsPage').then((module) => ({ default: module.RemoteAccessSettingsPage })))

export default function App() {
  const { status, loading, error, refresh } = useNabu()

  if (loading && !status) {
    return <main className="flex h-dvh items-center justify-center bg-canvas px-5"><PageLoading /></main>
  }
  if (!status) {
    return <main className="flex h-dvh items-center justify-center bg-canvas p-5"><div className="w-full max-w-lg"><ConnectionError message={error ?? 'Check that the local daemon is running.'} onRetry={() => void refresh()} /></div></main>
  }
  if (!status.setupComplete) return <SetupPage />

  return (
    <StartupIntro version={status.version}><AppShell>
      <Suspense fallback={<PageLoading label="Loading page…" />}><Routes>
        <Route path="/" element={<Navigate to="/chat" replace />} />
        <Route path="/tasks" element={<ContextSetupGate><TasksPage /></ContextSetupGate>} />
        <Route path="/tasks/:id" element={<ContextSetupGate><TaskDetailPage /></ContextSetupGate>} />
        <Route path="/calendar" element={<ContextSetupGate><CalendarPage /></ContextSetupGate>} />
        <Route path="/database" element={<ContextSetupGate><DatabasePage /></ContextSetupGate>} />
        <Route path="/database/:id" element={<ContextSetupGate><DatabaseDatasetPage /></ContextSetupGate>} />
        <Route path="/apps" element={<ContextSetupGate><AppsPage /></ContextSetupGate>} />
        <Route path="/outputs" element={<ContextSetupGate><OutputsPage /></ContextSetupGate>} />
        <Route path="/runs/:id" element={<ContextSetupGate><RunDetailPage /></ContextSetupGate>} />
        <Route path="/reports" element={<ContextSetupGate><ReportsPage /></ContextSetupGate>} />
        <Route path="/reports/:id" element={<ContextSetupGate><ReportDetailPage /></ContextSetupGate>} />
        <Route path="/approvals/:id" element={<ApprovalDetailPage />} />
        <Route path="/chat" element={<ChatPage />} />
        <Route path="/settings" element={<SettingsLayout />}>
          <Route index element={<OperatorSettingsPage />} />
          <Route path="workspaces" element={<WorkspacesSettingsPage />} />
          <Route path="policy" element={<PolicySettingsPage />} />
          <Route path="schedules" element={<ContextSetupGate><SchedulesSettingsPage /></ContextSetupGate>} />
          <Route path="scripts" element={<ContextSetupGate><ScriptsSettingsPage /></ContextSetupGate>} />
          <Route path="memory" element={<MemorySettingsPage />} />
          <Route path="soul" element={<SoulSettingsPage />} />
          <Route path="secrets" element={<IntegrationsSettingsPage />} />
          <Route path="mcp" element={<MCPSettingsPage />} />
          <Route path="integrations" element={<Navigate to="/settings/secrets" replace />} />
          <Route path="remote-access" element={<RemoteAccessSettingsPage />} />
        </Route>
        <Route path="*" element={<Navigate to="/chat" replace />} />
      </Routes></Suspense>
    </AppShell></StartupIntro>
  )
}
