import { useState } from 'react'
import { api, Me } from './api'

interface Props {
  me: Me
  onChange: (me: Me) => void
  onError: (message: string | null) => void
}

/**
 * The "who you are" half of the config page. This name is what gets matched
 * against a group's participants when you add one, so it is worth getting right
 * before adding groups.
 */
export function IdentityCard({ me, onChange, onError }: Props) {
  const [name, setName] = useState(me.display_name)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)

  const dirty = name.trim() !== me.display_name

  async function save() {
    setSaving(true)
    onError(null)
    try {
      const updated = await api.setDisplayName(name.trim())
      onChange(updated)
      setSaved(true)
      setTimeout(() => setSaved(false), 2000)
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
          onClick={save}
        >
          {saving ? 'Saving…' : saved ? 'Saved' : 'Save'}
        </button>
      </div>
    </section>
  )
}
