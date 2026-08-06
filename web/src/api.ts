// Typed wrappers over the spliit-mcp config API.
//
// Every call is session-cookie authenticated; a 401 means the OIDC login has
// expired and the UI should send the user back through /auth/login.

export class UnauthorizedError extends Error {}

export interface Me {
  sub: string
  email: string
  display_name: string
}

export interface Server {
  id: string
  name: string
  base_url: string
  created_at: string
  updated_at: string
}

export interface Group {
  id: string
  server_id: string
  server_name: string
  spliit_group_id: string
  alias: string
  participant_id: string
  participant_name: string
  group_name: string
  currency: string
  needs_setup: boolean
}

export interface Participant {
  id: string
  name: string
}

export interface GroupPreview {
  group_id: string
  name: string
  currency: string
  participants: Participant[]
  /** Empty when the display name matched no participant, or more than one. */
  suggested_participant: string
  suggested_from_name: string
  suggestion_is_ambiguous: boolean
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    headers: { 'Content-Type': 'application/json', ...(init?.headers ?? {}) },
  })

  if (res.status === 401) {
    throw new UnauthorizedError('not signed in')
  }
  if (!res.ok) {
    // The API returns {"error": "..."} for everything it handles itself.
    let message = `request failed with ${res.status}`
    try {
      const body = await res.json()
      if (body?.error) message = body.error
    } catch {
      // Non-JSON error body; keep the status-based message.
    }
    throw new Error(message)
  }
  if (res.status === 204) {
    return undefined as T
  }
  return res.json() as Promise<T>
}

export const api = {
  getMe: () => request<Me>('/api/me'),

  setDisplayName: (display_name: string) =>
    request<Me>('/api/me', { method: 'PUT', body: JSON.stringify({ display_name }) }),

  listServers: () => request<{ servers: Server[] }>('/api/servers').then((r) => r.servers),

  createServer: (name: string, base_url: string) =>
    request<Server>('/api/servers', { method: 'POST', body: JSON.stringify({ name, base_url }) }),

  updateServer: (id: string, name: string, base_url: string) =>
    request<Server>(`/api/servers/${id}`, {
      method: 'PATCH',
      body: JSON.stringify({ name, base_url }),
    }),

  deleteServer: (id: string) => request<void>(`/api/servers/${id}`, { method: 'DELETE' }),

  listGroups: () => request<{ groups: Group[] }>('/api/groups').then((r) => r.groups),

  previewGroup: (server_id: string, group_id: string) =>
    request<GroupPreview>('/api/groups/preview', {
      method: 'POST',
      body: JSON.stringify({ server_id, group_id }),
    }),

  createGroup: (server_id: string, group_id: string, alias: string, participant_id: string) =>
    request<Group>('/api/groups', {
      method: 'POST',
      body: JSON.stringify({ server_id, group_id, alias, participant_id }),
    }),

  updateGroup: (id: string, patch: { alias?: string; participant_id?: string }) =>
    request<Group>(`/api/groups/${id}`, { method: 'PATCH', body: JSON.stringify(patch) }),

  deleteGroup: (id: string) => request<void>(`/api/groups/${id}`, { method: 'DELETE' }),

  logout: () => request<void>('/auth/logout', { method: 'POST' }),
}
