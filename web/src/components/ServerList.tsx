import { useState } from 'react'
import { AppData } from '../App'
import { api } from '../api'

/**
 * Spliit instances this user can pull groups from — the public spliit.app plus
 * any self-hosted ones.
 */
export function ServerList({ data }: { data: AppData }) {
  const { servers, groups, reload: onChanged, onError } = data

  const [name, setName] = useState('')
  const [baseUrl, setBaseUrl] = useState('')
  const [busy, setBusy] = useState(false)

  async function add() {
    setBusy(true)
    onError(null)
    try {
      await api.createServer(name.trim(), baseUrl.trim())
      setName('')
      setBaseUrl('')
      onChanged()
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  async function remove(id: string) {
    setBusy(true)
    onError(null)
    try {
      await api.deleteServer(id)
      onChanged()
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="card">
      <h2>Spliit servers</h2>
      <p className="muted">
        The tRPC base URL of each instance, e.g.{' '}
        <code>https://spliit.app/api/trpc</code>.
      </p>

      <ul className="list">
        {servers.map((s) => {
          const inUse = groups.filter((g) => g.server_id === s.id).length
          return (
            <li key={s.id} className="list-item">
              <div className="list-main">
                <div className="list-title">{s.name}</div>
                <div className="muted small">
                  {s.base_url}
                  {inUse > 0 && ` · ${inUse} group${inUse === 1 ? '' : 's'}`}
                </div>
              </div>
              <div className="list-actions">
                <button
                  className="button danger"
                  onClick={() => remove(s.id)}
                  disabled={busy || inUse > 0}
                  title={inUse > 0 ? 'Remove its groups first' : undefined}
                >
                  Remove
                </button>
              </div>
            </li>
          )
        })}
      </ul>

      <div className="row">
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Name, e.g. home"
          aria-label="Server name"
        />
        <input
          value={baseUrl}
          onChange={(e) => setBaseUrl(e.target.value)}
          placeholder="https://spliit.example.com/api/trpc"
          aria-label="Server base URL"
        />
        <button
          className="button primary"
          onClick={add}
          disabled={busy || !name.trim() || !baseUrl.trim()}
        >
          Add server
        </button>
      </div>
    </section>
  )
}
