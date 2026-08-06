import { useState } from 'react'
import { api, Group } from './api'

interface Props {
  groups: Group[]
  onChanged: () => void
  onError: (message: string | null) => void
}

export function GroupList({ groups, onChanged, onError }: Props) {
  return (
    <section className="card">
      <h2>Available groups</h2>
      <p className="muted">
        These are the only groups your MCP client can reach. Removing one here
        unlinks it locally; nothing is deleted in Spliit.
      </p>

      {groups.length === 0 ? (
        <p className="muted">No groups yet.</p>
      ) : (
        <ul className="list">
          {groups.map((g) => (
            <GroupRow key={g.id} group={g} onChanged={onChanged} onError={onError} />
          ))}
        </ul>
      )}
    </section>
  )
}

function GroupRow({ group, onChanged, onError }: { group: Group } & Omit<Props, 'groups'>) {
  const [alias, setAlias] = useState(group.alias)
  const [editing, setEditing] = useState(false)
  const [busy, setBusy] = useState(false)

  async function saveAlias() {
    setBusy(true)
    onError(null)
    try {
      await api.updateGroup(group.id, { alias: alias.trim() })
      setEditing(false)
      onChanged()
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  async function remove() {
    setBusy(true)
    onError(null)
    try {
      await api.deleteGroup(group.id)
      onChanged()
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <li className="list-item">
      <div className="list-main">
        {editing ? (
          <div className="row">
            <input value={alias} onChange={(e) => setAlias(e.target.value)} aria-label="Alias" />
            <button className="button primary" onClick={saveAlias} disabled={busy || !alias.trim()}>
              Save
            </button>
            <button
              className="button"
              onClick={() => {
                setAlias(group.alias)
                setEditing(false)
              }}
              disabled={busy}
            >
              Cancel
            </button>
          </div>
        ) : (
          <>
            <div className="list-title">
              <code>{group.alias}</code>
              <span className="muted"> — {group.group_name}</span>
            </div>
            <div className="muted small">
              {group.server_name} · {group.currency} ·{' '}
              {group.participant_name ? (
                <>you are <strong>{group.participant_name}</strong></>
              ) : (
                <span className="warn">no participant pinned as you</span>
              )}
            </div>
          </>
        )}
      </div>

      {!editing && (
        <div className="list-actions">
          <button className="button" onClick={() => setEditing(true)} disabled={busy}>
            Rename
          </button>
          <button className="button danger" onClick={remove} disabled={busy}>
            Remove
          </button>
        </div>
      )}
    </li>
  )
}
