import { inject, Injectable } from '@angular/core';
import { MemberType, Role } from '../../gen/flock/api/config/permission/v1/permission_pb';
import { Api } from '../core/api';
import { KindName, kindOf, kindWord, Resource, resourceId, typeUrl } from '../core/resources';
import { Session } from '../core/session';

export interface Denial {
  reason: string;
}

export type RoleCache = Map<string, Role | 'none'>;

export interface ControllerFeatures {
  ipamEnabled: boolean;
}

type Action = 'create' | 'update' | 'delete';

const roleWords: Record<Role, string> = {
  [Role.READER]: 'reader',
  [Role.WRITER]: 'writer',
  [Role.OWNER]: 'owner',
};

const actionWords: Record<Role, string> = {
  [Role.READER]: 'Reading',
  [Role.WRITER]: 'Editing',
  [Role.OWNER]: 'Deleting',
};

// adminOnly is what the controller refuses to everyone but admins, known from the session without any call
export function adminOnly(kind: KindName, action: Action, features: ControllerFeatures): boolean {
  if (kind === 'zone') {
    return true;
  }
  if (kind !== 'vip') {
    return false;
  }
  // with ipam on, a non-admin creates a VIP through RegisterVip
  const createdByRegister = action === 'create' && features.ipamEnabled;
  return (action === 'create' || action === 'delete') && !createdByRegister;
}

// adminReason is why a user who is not an admin can't take an admin-only action, empty when they can
export function adminReason(
  kind: KindName,
  action: Action,
  features: ControllerFeatures,
  isAdmin: boolean,
): string {
  if (isAdmin || !adminOnly(kind, action, features)) {
    return '';
  }
  return `Only an admin of the controller can ${action} a ${kindWord(kind)}.`;
}

@Injectable({ providedIn: 'root' })
export class Access {
  private api = inject(Api);
  private session = inject(Session);
  private featuresRead: Promise<ControllerFeatures> | null = null;

  // features are read once per session, they only change with the controller configuration
  features(): Promise<ControllerFeatures> {
    this.featuresRead ??= this.api.info
      .getControllerFeatures({})
      .then((features) => ({ ipamEnabled: features.ipamEnabled }))
      .catch((error) => {
        this.featuresRead = null;
        throw error;
      });
    return this.featuresRead;
  }

  // check says why the caller lacks the needed role on the resource, null when the action may go on. HasPermission is
  // not used because with a USER member it ignores the roles held through groups. The cache belongs to the caller
  // (one per open panel) so a role change shows up when the panel is opened again. The action word is only the
  // default of the role, "Changing permissions" needs the owner role too.
  async check(
    resource: Resource,
    needed: Role,
    cache: RoleCache,
    action: string = actionWords[needed],
  ): Promise<Denial | null> {
    const user = this.session.user();
    if (user?.isAdmin) {
      return null;
    }
    const uuid = resourceId(resource);
    const held = cache.get(uuid) ?? (await this.heldRole(resource, user, cache));
    const isEnough = held !== 'none' && held >= needed;
    if (isEnough) {
      return null;
    }
    const roleWord = held === 'none' ? 'user with no role' : roleWords[held];
    return {
      reason: `You are a ${roleWord} of ${resource.name}. ${action} needs the ${roleWords[needed]} role, ask an owner.`,
    };
  }

  private async heldRole(
    resource: Resource,
    user: { username: string; groups: string[] } | null,
    cache: RoleCache,
  ): Promise<Role | 'none'> {
    const response = await this.api.permission.listMemberPermissions({
      resourceType: typeUrl(kindOf(resource)),
      resourceUuid: resourceId(resource),
      seeAll: true,
    });
    const roles = response.memberPermissions
      .filter((perm) => {
        const member = perm.member;
        const isUser =
          member?.memberType === MemberType.USER && member.memberName === user?.username;
        const isGroup =
          member?.memberType === MemberType.GROUP &&
          (user?.groups ?? []).includes(member.memberName);
        return isUser || isGroup;
      })
      .map((perm) => perm.role);
    const held = roles.length > 0 ? (Math.max(...roles) as Role) : 'none';
    cache.set(resourceId(resource), held);
    return held;
  }
}
