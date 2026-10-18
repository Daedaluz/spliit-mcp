import { useState } from 'react'
import { AppData } from '../App'
import { api } from '../api'

/**
 * The "who you are" half of the config page. The name is what gets matched
 * against a group's participants when you add one, so it is worth getting right
 * before adding groups. The default group is where MCP tools act when a request
 * names no group, e.g. "add 120 for pizza".
 */
export function IdentityCard({ data }: { data: AppData }) {
  const { me, setMe, groups, onError } = data

  const [name, setName] = useState(me.display_name)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)

  const dirty = name.trim() !== me.display_name

  async function update(action: () => Promise<typeof me>, flash: boolean) {
    setSaving(true)
    onError(null)
    try {
      setMe(await action())
      if (flash) {
        setSaved(true)
        setTimeout(() => setSaved(false), 2000)
      }
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <section className="card">
      <h2>You</h2>
      <p className="muted">
        The name you go by in Spliit. When you add a group, the participant with
        this name is pinned as you; if none matches, you pick one.
      </p>
      <div className="row">
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Your name in Spliit"
          aria-label="Your display name"
        />
        <button
          className="button primary"
          disabled={!dirty || !name.trim() || saving}
          onClick={() => update(() => api.setDisplayName(name.trim()), true)}
        >
          {saving ? 'Saving…' : saved ? 'Saved' : 'Save'}
        </button>
      </div>

      <p className="muted card-section">
        Your default group. When you ask your MCP client to add an expense without
        saying where, it goes here.
      </p>
      <div className="row">
        <select
          value={me.default_group_id}
          disabled={saving || groups.length === 0}
          onChange={(e) => update(() => api.setDefaultGroup(e.target.value), false)}
          aria-label="Default group"
        >
          <option value="">{groups.length === 0 ? 'Join a group first' : 'No default — always ask'}</option>
          {groups.map((g) => (
            <option key={g.id} value={g.id}>
              {g.alias} — {g.group_name}
            </option>
          ))}
        </select>
      </div>
    </section>
  )
}
