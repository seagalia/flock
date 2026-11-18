import { create } from '@bufbuild/protobuf';
import { VIPSchema } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { VirtualServerSchema } from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { ZoneSchema } from '../../gen/flock/api/config/zone/v1/zone_pb';
import { ListFilter_Operator } from '../../gen/flock/services/core/v1/filter_pb';
import {
  cliCommand,
  kindOf,
  kinds,
  linkedToFilter,
  listAll,
  matchesSearch,
  nameOrUuidFilter,
  resourceId,
} from './resources';

describe('resources', () => {
  it('searches a uuid exactly and a name with like', () => {
    const byUuid = nameOrUuidFilter('5f1e1bb7-2f6c-43c1-a048-c77e48e9cbe5');
    expect(byUuid.key).toBe('uuid');
    expect(byUuid.operator).toBe(ListFilter_Operator.EQUAL);

    const byName = nameOrUuidFilter('backend');
    expect(byName.key).toBe('name');
    expect(byName.operator).toBe(ListFilter_Operator.LIKE);
  });

  it('filters linked resources by the type url of the resource', () => {
    const vip = create(VIPSchema, { UUID: 'vip-uuid', name: 'vip' });
    const filter = linkedToFilter(vip);
    expect(filter.key).toBe('type.googleapis.com/flock.api.config.vip.v1.VIP');
    expect(filter.value).toBe('vip-uuid');
    expect(kindOf(vip).link).toBe('/vips');
  });

  it('identifies zones by their uuid', () => {
    expect(resourceId(create(ZoneSchema, { UUID: 'zone-uuid', name: 'eu-west' }))).toBe(
      'zone-uuid',
    );
  });

  it('pages until a page is not full', async () => {
    const offsets: number[] = [];
    const all = await listAll(async (limit, offset) => {
      offsets.push(offset);
      const left = 230 - offset;
      return Array.from({ length: Math.min(limit, left) }, (_, index) => offset + index);
    });
    expect(all.length).toBe(230);
    expect(offsets).toEqual([0, 100, 200]);
  });

  it('applies the search of the table to a loaded list like the api does', () => {
    const vs = create(VirtualServerSchema, {
      UUID: '5f1e1bb7-2f6c-43c1-a048-c77e48e9cbe5',
      name: 'Web-Front',
    });

    expect(matchesSearch(vs, [])).toBe(true);
    expect(matchesSearch(vs, [nameOrUuidFilter('web')])).toBe(true);
    expect(matchesSearch(vs, [nameOrUuidFilter('api')])).toBe(false);
    expect(matchesSearch(vs, [nameOrUuidFilter('5f1e1bb7-2f6c-43c1-a048-c77e48e9cbe5')])).toBe(
      true,
    );
  });

  it('gives the cli command reading a resource, quoting names the shell would split', () => {
    const vs = (name: string) => create(VirtualServerSchema, { name });

    expect(cliCommand(kinds.vs, vs('web-front'))).toBe('flock vs web-front');
    expect(cliCommand(kinds.vip, create(VIPSchema, { name: '0.0.0.0/0' }))).toBe(
      'flock vip 0.0.0.0/0',
    );
    expect(cliCommand(kinds.vs, vs("bob's web"))).toBe(`flock vs 'bob'\\''s web'`);
    expect(cliCommand(kinds.endpoint, vs('web'))).toBeNull();
  });
});
