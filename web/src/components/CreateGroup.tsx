import { useState } from 'react'
import { AppData } from '../App'
import { api } from '../api'

/**
 * Creates a new group in Spliit and joins it in one step.
 *
 * These are deliberately not separable: a group that exists in Spliit but was
 * never registered here is unreachable, and Spliit offers no way to list groups
 * to find it again.
 *
 * Creating is the one place with no group link to derive an instance from, so
 * the target is offered explicitly — defaulted, and suggesting the instances
 * already in use.
 */
export function CreateGroup({ data }: { data: AppData }) {
  const { groups, me, reload, onError } = data

  // Instances already in use are the realistic options, and the empty default
  // means "this server's configured instance".
  const knownInstances = Array.from(new Set(groups.map((g) => g.base_url))).sort()

  const [baseUrl, setBaseUrl] = useState('')
  const [name, setName] = useState('')
  const [currency, setCurrency] = useState('USD')
  const [yourName, setYourName] = useState(me.display_name)
  const [others, setOthers] = useState('')
  const [busy, setBusy] = useState(false)

  async function create() {
    setBusy(true)
    onError(null)
    try {
      await api.createGroup({
        base_url: baseUrl.trim(),
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

  return (
    <section className="card">
      <h2>Create a group</h2>
      <p className="muted">
        Creates the group in Spliit and joins it here. You are added as a
        participant automatically.
      </p>

      <div className="row">
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

      <label className="field">
        <span>Spliit instance — leave empty for the default</span>
        <input
          value={baseUrl}
          onChange={(e) => setBaseUrl(e.target.value)}
          placeholder="https://spliit.example.com/api/trpc"
          list="known-instances"
          aria-label="Spliit instance tRPC URL"
        />
        <datalist id="known-instances">
          {knownInstances.map((url) => (
            <option key={url} value={url} />
          ))}
        </datalist>
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
