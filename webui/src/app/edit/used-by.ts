import { Api } from '../core/api';
import {
  KindName,
  kindOf,
  linkedToFilter,
  listAll,
  Resource,
  usedByKinds,
} from '../core/resources';

export interface UsedBy {
  kind: KindName;
  resource: Resource;
}

// usedBy lists what the caller can read among the resources using this one. The controller refuses a deletion for
// every user, readable or not, so an empty list does not promise the deletion will pass.
export async function usedBy(
  api: Api,
  resource: Resource,
  kindNames: KindName[] = usedByKinds[kindOf(resource).name],
): Promise<UsedBy[]> {
  const filters = [linkedToFilter(resource)];
  const lists = kindNames.map(async (kind) => {
    const found = await listAll((limit, offset) => api.list(kind, { limit, offset, filters }));
    return found.map((user) => ({ kind, resource: user }));
  });
  return (await Promise.all(lists)).flat();
}
