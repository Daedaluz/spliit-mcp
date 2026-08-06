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

/** How to reach this server, as the server itself understands it. */
export interface ServerConfig {
  mcp_url: string
  issuer: string
  /** Empty unless a client was pre-registered for a provider without DCR. */
  mcp_client_id: string
}

export interface Group {
  id: string
  /** tRPC base URL of the instance hosting this group, derived on join. */
  base_url: string
  /** Display form of base_url, e.g. "spliit.app". */
  host: string
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
  base_url: string
  host: string
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

  getConfig: () => request<ServerConfig>('/api/config'),

  setDisplayName: (display_name: string) =>
    request<Me>('/api/me', { method: 'PUT', body: JSON.stringify({ display_name }) }),

  listGroups: () => request<{ groups: Group[] }>('/api/groups').then((r) => r.groups),

  /**
   * Look a group up without joining. The hosting instance is derived from
   * group_id when it is a full URL; base_url overrides that.
   */
  previewGroup: (group_id: string, base_url = '') =>
    request<GroupPreview>('/api/groups/preview', {
      method: 'POST',
      body: JSON.stringify({ group_id, base_url }),
    }),

  /** Join an existing Spliit group. participant_id is required — see the API. */
  joinGroup: (group_id: string, base_url: string, alias: string, participant_id: string) =>
    request<Group>('/api/groups', {
      method: 'POST',
      body: JSON.stringify({ group_id, base_url, alias, participant_id }),
    }),

  /** Create a brand new group in Spliit and join it in one step. */
  createGroup: (body: {
    base_url: string
    name: string
    currency: string
    alias: string
    participants: string[]
    your_name: string
  }) => request<Group>('/api/groups/new', { method: 'POST', body: JSON.stringify(body) }),

  updateGroup: (id: string, patch: { alias?: string; participant_id?: string }) =>
    request<Group>(`/api/groups/${id}`, { method: 'PATCH', body: JSON.stringify(patch) }),

  deleteGroup: (id: string) => request<void>(`/api/groups/${id}`, { method: 'DELETE' }),

  logout: () => request<void>('/auth/logout', { method: 'POST' }),
}
