import { useState } from 'react'
import { AppData } from '../App'
import { api, Group, Participant } from '../api'

export function GroupList({ data }: { data: AppData }) {
  return (
    <ul className="list">
      {data.groups.map((g) => (
        <GroupRow key={g.id} group={g} data={data} />
      ))}
    </ul>
  )
}

function GroupRow({ group, data }: { group: Group; data: AppData }) {
  const { reload, onError } = data

  const [alias, setAlias] = useState(group.alias)
  const [mode, setMode] = useState<'view' | 'rename' | 'participant'>('view')
  const [participants, setParticipants] = useState<Participant[]>([])
  const [busy, setBusy] = useState(false)

  async function run(action: () => Promise<unknown>) {
    setBusy(true)
    onError(null)
    try {
      await action()
      setMode('view')
      await reload()
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  // Re-picking who you are needs the group's current participant list, which
  // only Spliit knows — and it may have changed since the group was joined.
  async function openParticipantPicker() {
    setBusy(true)
    onError(null)
    try {
      const preview = await api.previewGroup(group.spliit_group_id, group.base_url)
      setParticipants(preview.participants)
      setMode('participant')
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <li className="list-item">
      <div className="list-main">
        {mode === 'rename' ? (
          <div className="row">
            <input value={alias} onChange={(e) => setAlias(e.target.value)} aria-label="Alias" />
            <button
              className="button primary"
              disabled={busy || !alias.trim()}
              onClick={() => run(() => api.updateGroup(group.id, { alias: alias.trim() }))}
            >
              Save
            </button>
            <button
              className="button"
              disabled={busy}
              onClick={() => {
                setAlias(group.alias)
                setMode('view')
              }}
            >
              Cancel
            </button>
          </div>
        ) : mode === 'participant' ? (
          <fieldset className="field">
            <legend>Which participant are you in {group.group_name}?</legend>
            <div className="choices">
              {participants.map((p) => (
                <button
                  key={p.id}
                  className={p.id === group.participant_id ? 'button primary' : 'button'}
                  disabled={busy}
                  onClick={() => run(() => api.updateGroup(group.id, { participant_id: p.id }))}
                >
                  {p.name}
                </button>
              ))}
            </div>
            <div className="row">
              <button className="button" disabled={busy} onClick={() => setMode('view')}>
                Cancel
              </button>
            </div>
          </fieldset>
        ) : (
          <>
            <div className="list-title">
              <code>{group.alias}</code>
              <span className="muted"> — {group.group_name}</span>
            </div>
            <div className="muted small">
              {group.host} · {group.currency} ·{' '}
              {group.participant_name ? (
                <>
                  you are <strong>{group.participant_name}</strong>
                </>
              ) : (
                <span className="warn">no participant set as you</span>
              )}
            </div>
          </>
        )}
      </div>

      {mode === 'view' && (
        <div className="list-actions">
          <button className="button" onClick={openParticipantPicker} disabled={busy}>
            {group.participant_name ? 'Change you' : 'Set you'}
          </button>
          <button className="button" onClick={() => setMode('rename')} disabled={busy}>
            Rename
          </button>
          <button
            className="button danger"
            disabled={busy}
            onClick={() => run(() => api.deleteGroup(group.id))}
          >
            Remove
          </button>
        </div>
      )}
    </li>
  )
}
