import { useRef, useState } from 'react'

/**
 * A read-only value with a copy button.
 *
 * navigator.clipboard is only available in a secure context, and this server is
 * routinely reached over plain HTTP on a LAN address — so the modern API is
 * tried first and a selection-based copy is used when it is unavailable. If
 * both fail the text is at least left selected for a manual copy.
 */
export function CopyField({ label, value }: { label?: string; value: string }) {
  const [copied, setCopied] = useState(false)
  const ref = useRef<HTMLTextAreaElement>(null)

  async function copy() {
    const node = ref.current
    if (node) {
      node.select()
    }

    let ok = false
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(value)
        ok = true
      } else if (node) {
        ok = document.execCommand('copy')
      }
    } catch {
      ok = false
    }

    if (ok) {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    }
  }

  return (
    <div className="copy-field">
      {label && <span className="copy-label">{label}</span>}
      <div className="copy-row">
        <textarea
          ref={ref}
          className="copy-value"
          readOnly
          rows={value.length > 70 ? 3 : 1}
          value={value}
          onFocus={(e) => e.currentTarget.select()}
          aria-label={label ?? 'Value to copy'}
        />
        <button className="button" onClick={copy}>
          {copied ? 'Copied' : 'Copy'}
        </button>
      </div>
    </div>
  )
}
