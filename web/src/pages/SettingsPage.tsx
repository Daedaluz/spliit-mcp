import { AppData } from '../App'
import { IdentityCard } from '../components/IdentityCard'
import { JoinGroup } from '../components/JoinGroup'
import { CreateGroup } from '../components/CreateGroup'

/**
 * Everything that changes what exists: who you are, and joining, creating or
 * removing groups. There is no instance registry — a group link says which
 * Spliit instance hosts it.
 */
export function SettingsPage({ data }: { data: AppData }) {
  return (
    <>
      <IdentityCard data={data} />
      <JoinGroup data={data} />
      <CreateGroup data={data} />
    </>
  )
}
