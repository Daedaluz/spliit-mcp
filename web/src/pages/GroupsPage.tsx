import { Link } from 'react-router-dom'
import { AppData } from '../App'
import { GroupList } from '../components/GroupList'

/**
 * The overview: what your MCP client can currently reach. Everything that
 * changes membership lives on the settings page.
 */
export function GroupsPage({ data }: { data: AppData }) {
  const broken = data.groups.filter((g) => g.needs_setup)

  return (
    <>
      <section className="card">
        <h2>Available groups</h2>
        <p className="muted">
          These are the only groups your MCP client can reach. Join, create or
          remove them in <Link to="/settings">Settings</Link>.
        </p>

        {broken.length > 0 && (
          <div className="banner warn-banner">
            {broken.length === 1 ? 'One group has' : `${broken.length} groups have`} no
            participant set as you, so expenses cannot be recorded in{' '}
            {broken.length === 1 ? 'it' : 'them'}. Fix that below.
          </div>
        )}

        {data.groups.length === 0 ? (
          <p className="muted">
            No groups yet — <Link to="/settings">join or create one</Link>.
          </p>
        ) : (
          <GroupList data={data} />
        )}
      </section>

      <section className="card">
        <h2>Connecting a client</h2>
        <p className="muted">Point an MCP client at this server:</p>
        <pre className="snippet">
          claude mcp add --transport http spliit {window.location.origin}/mcp
        </pre>
      </section>
    </>
  )
}
