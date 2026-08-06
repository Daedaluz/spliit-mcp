import { useState } from 'react'
import { AppData } from '../App'
import { api } from '../api'

/**
 * Creates a new group in Spliit and joins it in one step.
 *
 * These are deliberately not separable: a group that exists in Spliit but was
 * never registered here is unreachable, and Spliit offers no way to list groups
 * to find it again.
 */
export function CreateGroup({ data }: { data: AppData }) {
  const { servers, me, reload, onError } = data

  const [serverId, setServerId] = useState('')
  const [name, setName] = useState('')
  const [currency, setCurrency] = useState('USD')
  const [yourName, setYourName] = useState(me.display_name)
  const [others, setOthers] = useState('')
  const [busy, setBusy] = useState(false)

  const effectiveServerId = serverId || servers[0]?.id || ''

  async function create() {
    setBusy(true)
    onError(null)
    try {
      await api.createGroup({
        server_id: effectiveServerId,
        name: name.trim(),
        currency: currency.trim(),
        alias: '',
        your_name: yourName.trim(),
        // One name per line, blanks dropped.
        participants: others
          .split('\n')
          .map((s) => s.trim())
          .filter(Boolean),
      })
      setName('')
      setOthers('')
      await reload()
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  if (servers.length === 0) {
    return null
  }

  return (
    <section className="card">
      <h2>Create a group</h2>
      <p className="muted">
        Creates the group in Spliit and joins it here. You are added as a
        participant automatically.
      </p>

      <div className="row">
        {servers.length > 1 && (
          <select
            value={effectiveServerId}
            onChange={(e) => setServerId(e.target.value)}
            aria-label="Spliit server"
          >
            {servers.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </select>
        )}
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Group name"
          aria-label="Group name"
        />
        <input
          value={currency}
          onChange={(e) => setCurrency(e.target.value)}
          placeholder="USD"
          aria-label="Currency"
          className="narrow"
        />
      </div>

      <label className="field">
        <span>Your name in this group</span>
        <input
          value={yourName}
          onChange={(e) => setYourName(e.target.value)}
          placeholder="How you appear in Spliit"
        />
      </label>

      <label className="field">
        <span>Other participants, one per line</span>
        <textarea
          value={others}
          onChange={(e) => setOthers(e.target.value)}
          rows={3}
          placeholder={'Anna\nErik'}
        />
      </label>

      <div className="row">
        <button
          className="button primary"
          disabled={busy || name.trim().length < 2 || yourName.trim().length < 2}
          onClick={create}
        >
          {busy ? 'Creating…' : 'Create group'}
        </button>
      </div>
    </section>
  )
}
