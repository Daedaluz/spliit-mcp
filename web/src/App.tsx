import { useCallback, useEffect, useState } from 'react'
import { api, Group, Me, Server, UnauthorizedError } from './api'
import { IdentityCard } from './IdentityCard'
import { ServerList } from './ServerList'
import { GroupList } from './GroupList'
import { AddGroup } from './AddGroup'

export function App() {
  const [me, setMe] = useState<Me | null>(null)
  const [servers, setServers] = useState<Server[]>([])
  const [groups, setGroups] = useState<Group[]>([])
  const [loading, setLoading] = useState(true)
  const [signedOut, setSignedOut] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const reload = useCallback(async () => {
    try {
      const [meResult, serverResult, groupResult] = await Promise.all([
        api.getMe(),
        api.listServers(),
        api.listGroups(),
      ])
      setMe(meResult)
      setServers(serverResult)
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
    return <main className="page"><p className="muted">Loading…</p></main>
  }

  if (signedOut || !me) {
    return (
      <main className="page center">
        <h1>Spliit MCP</h1>
        <p className="muted">
          Sign in to manage which Spliit groups are available to your MCP client.
        </p>
        <a className="button primary" href="/auth/login">Sign in</a>
      </main>
    )
  }

  return (
    <main className="page">
      <header className="header">
        <h1>Spliit MCP</h1>
        <div className="header-actions">
          <span className="muted">{me.email || me.sub}</span>
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

      {error && <div className="banner error">{error}</div>}

      <IdentityCard me={me} onChange={setMe} onError={setError} />

      <ServerList servers={servers} groups={groups} onChanged={reload} onError={setError} />

      <AddGroup servers={servers} onAdded={reload} onError={setError} />

      <GroupList groups={groups} onChanged={reload} onError={setError} />
    </main>
  )
}
