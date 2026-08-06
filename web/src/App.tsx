import { useCallback, useEffect, useState } from 'react'
import { NavLink, Route, Routes } from 'react-router-dom'
import { api, Group, Me, ServerConfig, UnauthorizedError } from './api'
import { GroupsPage } from './pages/GroupsPage'
import { SettingsPage } from './pages/SettingsPage'

/** Everything the pages read, reloaded together after any mutation. */
export interface AppData {
  me: Me
  config: ServerConfig
  groups: Group[]
  reload: () => Promise<void>
  onError: (message: string | null) => void
  setMe: (me: Me) => void
}

export function App() {
  const [me, setMe] = useState<Me | null>(null)
  const [config, setConfig] = useState<ServerConfig | null>(null)
  const [groups, setGroups] = useState<Group[]>([])
  const [loading, setLoading] = useState(true)
  const [signedOut, setSignedOut] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const reload = useCallback(async () => {
    try {
      const [meResult, configResult, groupResult] = await Promise.all([
        api.getMe(),
        api.getConfig(),
        api.listGroups(),
      ])
      setMe(meResult)
      setConfig(configResult)
      setGroups(groupResult)
      setSignedOut(false)
      setError(null)
    } catch (err) {
      if (err instanceof UnauthorizedError) {
        setSignedOut(true)
      } else {
        setError(err instanceof Error ? err.message : String(err))
      }
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void reload()
  }, [reload])

  if (loading) {
    return (
      <main className="page">
        <p className="muted">Loading…</p>
      </main>
    )
  }

  if (signedOut || !me || !config) {
    return (
      <main className="page center">
        <h1>Spliit MCP</h1>
        <p className="muted">
          Sign in to manage which Spliit groups are available to your MCP client.
        </p>
        <a className="button primary" href="/auth/login">
          Sign in
        </a>
      </main>
    )
  }

  const data: AppData = { me, config, groups, reload, onError: setError, setMe }

  return (
    <main className="page">
      <header className="header">
        <h1>Spliit MCP</h1>
        <div className="header-actions">
          {/* The subject is an opaque UUID; only show it if nothing else exists. */}
          <span className="muted">{me.display_name || me.email || me.sub}</span>
          <button
            className="button"
            onClick={async () => {
              await api.logout()
              setSignedOut(true)
            }}
          >
            Sign out
          </button>
        </div>
      </header>

      <nav className="tabs">
        <NavLink to="/" end className={({ isActive }) => (isActive ? 'tab active' : 'tab')}>
          Groups
        </NavLink>
        <NavLink to="/settings" className={({ isActive }) => (isActive ? 'tab active' : 'tab')}>
          Settings
        </NavLink>
      </nav>

      {error && <div className="banner error">{error}</div>}

      <Routes>
        <Route path="/" element={<GroupsPage data={data} />} />
        <Route path="/settings" element={<SettingsPage data={data} />} />
        <Route path="*" element={<GroupsPage data={data} />} />
      </Routes>
    </main>
  )
}
