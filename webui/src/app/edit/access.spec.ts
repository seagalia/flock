import { TestBed } from '@angular/core/testing';
import { create } from '@bufbuild/protobuf';
import { BackendSchema } from '../../gen/flock/api/config/backend/v1/backend_pb';
import {
  MemberPermissionSchema,
  MemberType,
  Role,
} from '../../gen/flock/api/config/permission/v1/permission_pb';
import { Api } from '../core/api';
import { Session } from '../core/session';
import { Access, adminOnly, RoleCache } from './access';

function member(name: string, memberType: MemberType, role: Role) {
  return create(MemberPermissionSchema, { member: { memberName: name, memberType }, role });
}

describe('Access', () => {
  const backend = create(BackendSchema, { UUID: 'b1', name: 'web-https' });
  let listMemberPermissions: ReturnType<typeof vi.fn>;
  let getControllerFeatures: ReturnType<typeof vi.fn>;
  let user: { username: string; groups: string[]; isAdmin: boolean };
  let cache: RoleCache;

  beforeEach(() => {
    listMemberPermissions = vi.fn();
    getControllerFeatures = vi.fn(async () => ({ ipamEnabled: true }));
    user = { username: 'alice', groups: ['ops'], isAdmin: false };
    cache = new Map();
    TestBed.configureTestingModule({
      providers: [
        {
          provide: Api,
          useValue: {
            permission: { listMemberPermissions },
            info: { getControllerFeatures },
          },
        },
        { provide: Session, useValue: { user: () => user } },
      ],
    });
  });

  const check = (role: Role) => TestBed.inject(Access).check(backend, role, cache);

  it('lets an admin through without a call', async () => {
    user.isAdmin = true;

    expect(await check(Role.OWNER)).toBeNull();
    expect(listMemberPermissions).not.toHaveBeenCalled();
  });

  it('grants writer held through a group', async () => {
    listMemberPermissions.mockResolvedValue({
      memberPermissions: [
        member('alice', MemberType.USER, Role.READER),
        member('ops', MemberType.GROUP, Role.WRITER),
      ],
    });

    expect(await check(Role.WRITER)).toBeNull();
    expect(listMemberPermissions).toHaveBeenCalledWith(
      expect.objectContaining({ resourceUuid: 'b1', seeAll: true }),
    );
  });

  it('denies a reader the writer role', async () => {
    listMemberPermissions.mockResolvedValue({
      memberPermissions: [member('alice', MemberType.USER, Role.READER)],
    });

    expect(await check(Role.WRITER)).toEqual({
      reason: 'You are a reader of web-https. Editing needs the writer role, ask an owner.',
    });
  });

  it('names a user with no role and the deleting action', async () => {
    listMemberPermissions.mockResolvedValue({
      memberPermissions: [member('alice', MemberType.GROUP, Role.OWNER)],
    });

    expect(await check(Role.OWNER)).toEqual({
      reason:
        'You are a user with no role of web-https. Deleting needs the owner role, ask an owner.',
    });
  });

  it('asks once per resource', async () => {
    listMemberPermissions.mockResolvedValue({ memberPermissions: [] });

    await check(Role.WRITER);
    await check(Role.OWNER);

    expect(listMemberPermissions).toHaveBeenCalledTimes(1);
  });

  it('reads the controller features once', async () => {
    const access = TestBed.inject(Access);

    expect(await access.features()).toEqual({ ipamEnabled: true });
    await access.features();

    expect(getControllerFeatures).toHaveBeenCalledTimes(1);
  });
});

describe('adminOnly', () => {
  const noIpam = { ipamEnabled: false };
  const ipam = { ipamEnabled: true };

  it('reserves every zone action to admins', () => {
    expect(adminOnly('zone', 'create', ipam)).toBe(true);
    expect(adminOnly('zone', 'update', ipam)).toBe(true);
    expect(adminOnly('zone', 'delete', ipam)).toBe(true);
  });

  it('reserves vip create and delete, create goes through RegisterVip with ipam', () => {
    expect(adminOnly('vip', 'create', noIpam)).toBe(true);
    expect(adminOnly('vip', 'create', ipam)).toBe(false);
    expect(adminOnly('vip', 'delete', ipam)).toBe(true);
    expect(adminOnly('vip', 'update', noIpam)).toBe(false);
  });

  it('leaves the other kinds to roles', () => {
    expect(adminOnly('backend', 'delete', noIpam)).toBe(false);
    expect(adminOnly('instance', 'create', noIpam)).toBe(false);
  });
});
