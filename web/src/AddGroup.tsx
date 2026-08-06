import { useState } from 'react'
import { api, GroupPreview, Server } from './api'

interface Props {
  servers: Server[]
  onAdded: () => void
  onError: (message: string | null) => void
}

/**
 * Two-step add flow. Step one fetches the group from Spliit read-only so we can
 * show its real participants; step two commits it with the participant that
 * represents you.
 *
 * The lookup step exists because a group ID is the only credential Spliit has —
 * pasting a wrong one should fail visibly here, not silently produce a broken
 * row that only errors later during a tool call.
 */
export function AddGroup({ servers, onAdded, onError }: Props) {
  const [serverId, setServerId] = useState(servers[0]?.id ?? '')
  const [groupId, setGroupId] = useState('')
  const [preview, setPreview] = useState<GroupPreview | null>(null)
  const [alias, setAlias] = useState('')
  const [participantId, setParticipantId] = useState('')
  const [busy, setBusy] = useState(false)

  const effectiveServerId = serverId || servers[0]?.id || ''

  async function lookup() {
    setBusy(true)
    onError(null)
    try {
      const result = await api.previewGroup(effectiveServerId, groupId)
      setPreview(result)
      setAlias(result.name)
      setParticipantId(result.suggested_participant)
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
      setPreview(null)
    } finally {
      setBusy(false)
    }
  }

  async function add() {
    setBusy(true)
    onError(null)
    try {
      await api.createGroup(effectiveServerId, preview!.group_id, alias.trim(), participantId)
      setPreview(null)
      setGroupId('')
      setAlias('')
      setParticipantId('')
      onAdded()
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  if (servers.length === 0) {
    return (
      <section className="card">
        <h2>Add a group</h2>
        <p className="muted">Add a Spliit server first.</p>
      </section>
    )
  }

  return (
    <section className="card">
      <h2>Add a group</h2>

      {!preview ? (
        <>
          <p className="muted">
            Paste a group ID or the full Spliit group URL. Anyone with a group ID
            has full access to it, which is why they live behind your login here.
          </p>
          <div className="row">
            {servers.length > 1 && (
              <select
                value={effectiveServerId}
                onChange={(e) => setServerId(e.target.value)}
                aria-label="Spliit server"
              >
                {servers.map((s) => (
                  <option key={s.id} value={s.id}>{s.name}</option>
                ))}
              </select>
            )}
            <input
              value={groupId}
              onChange={(e) => setGroupId(e.target.value)}
              placeholder="Group ID or https://spliit.app/groups/…"
              aria-label="Group ID"
            />
            <button
              className="button primary"
              disabled={!groupId.trim() || busy}
              onClick={lookup}
            >
              {busy ? 'Looking up…' : 'Look up'}
            </button>
          </div>
        </>
      ) : (
        <>
          <p>
            Found <strong>{preview.name}</strong>{' '}
            <span className="muted">({preview.currency})</span>
          </p>

          <label className="field">
            <span>Alias for MCP tools</span>
            <input
              value={alias}
              onChange={(e) => setAlias(e.target.value)}
              placeholder="Short name used by the model"
            />
          </label>

          <fieldset className="field">
            <legend>Which participant are you?</legend>
            {preview.suggested_participant ? (
              <p className="muted">
                Matched <strong>{preview.suggested_from_name}</strong> automatically.
              </p>
            ) : (
              <p className="muted">
                {preview.suggestion_is_ambiguous
                  ? `More than one participant is called "${preview.suggested_from_name}" — pick the right one.`
                  : `No participant is called "${preview.suggested_from_name}" — pick yourself below.`}
              </p>
            )}
            <div className="choices">
              {preview.participants.map((p) => (
                <label key={p.id} className="choice">
                  <input
                    type="radio"
                    name="participant"
                    value={p.id}
                    checked={participantId === p.id}
                    onChange={() => setParticipantId(p.id)}
                  />
                  <span>{p.name}</span>
                </label>
              ))}
            </div>
          </fieldset>

          <div className="row">
            <button
              className="button primary"
              disabled={!alias.trim() || !participantId || busy}
              onClick={add}
            >
              {busy ? 'Adding…' : 'Add group'}
            </button>
            <button className="button" onClick={() => setPreview(null)} disabled={busy}>
              Cancel
            </button>
          </div>
        </>
      )}
    </section>
  )
}
