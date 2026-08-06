import { ServerConfig } from '../api'
import { CopyField } from './CopyField'

/**
 * Everything needed to point another machine's MCP client at this server.
 *
 * The endpoint comes from the server's configured public URL rather than the
 * browser's location, so the value copied here is the one that actually works
 * from elsewhere — behind a proxy, or when this page is open on localhost while
 * the client lives on another host.
 */
export function ConnectCard({ config }: { config: ServerConfig }) {
  const command = [
    'claude mcp add --transport http spliit',
    config.mcp_url,
    // Providers without dynamic client registration need the client named
    // explicitly, along with a fixed callback port to register beforehand.
    ...(config.mcp_client_id
      ? [`--client-id ${config.mcp_client_id}`, '--callback-port 45454']
      : []),
  ].join(' ')

  return (
    <section className="card">
      <h2>Connect a client</h2>
      <p className="muted">
        Sign in with the same account on whichever machine runs the client; the
        groups below are what it will see.
      </p>

      <CopyField label="MCP endpoint" value={config.mcp_url} />
      <CopyField label="Claude Code" value={command} />

      {config.mcp_client_id && (
        <p className="muted small">
          This server has a pre-registered OAuth client, so the command names it
          explicitly. Register <code>http://localhost:45454/callback</code> as a
          redirect URI on that client, or the login will be rejected.
        </p>
      )}
    </section>
  )
}
