import { AppData } from '../App'
import { IdentityCard } from '../components/IdentityCard'
import { ServerList } from '../components/ServerList'
import { JoinGroup } from '../components/JoinGroup'
import { CreateGroup } from '../components/CreateGroup'

/**
 * Everything that changes what exists: who you are, which Spliit instances are
 * registered, and joining, creating or removing groups.
 */
export function SettingsPage({ data }: { data: AppData }) {
  return (
    <>
      <IdentityCard data={data} />
      <JoinGroup data={data} />
      <CreateGroup data={data} />
      <ServerList data={data} />
    </>
  )
}
