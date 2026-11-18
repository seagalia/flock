import { create } from '@bufbuild/protobuf';
import { VIPSchema } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { InstanceSchema } from '../../gen/flock/api/config/instance/v1/instance_pb';
import { ZoneSchema } from '../../gen/flock/api/config/zone/v1/zone_pb';
import { resourceDocument, resourceFile } from './resource-file';

describe('resourceDocument', () => {
  it('writes a vip with its kind first and its UUID', () => {
    const vip = create(VIPSchema, { UUID: 'u1', name: 'front', ip: '10.0.0.1' });

    const text = resourceDocument(vip)!;

    expect(text.split('\n')[0]).toBe('kind: vip');
    expect(text).toContain('UUID: u1');
    expect(text).toContain('ip: 10.0.0.1');
  });

  it('has no document for an instance', () => {
    expect(resourceDocument(create(InstanceSchema, { name: 'i' }))).toBeNull();
  });
});

describe('resourceFile', () => {
  it('writes a vip as a flock apply document named after it', () => {
    const file = resourceFile(create(VIPSchema, { name: 'front', ip: '10.0.0.1' }));

    expect(file.name).toBe('front.yml');
    expect(file.text.split('\n').slice(0, 2)).toEqual(['# flock apply -f front.yml', 'kind: vip']);
  });

  it('writes a zone as the manifest of create-zone, with no kind', () => {
    const file = resourceFile(create(ZoneSchema, { name: 'dc1' }));

    expect(file.text.split('\n')[0]).toBe('# flock create-zone -f dc1.yml');
    expect(file.text).toContain('name: dc1');
    expect(file.text).not.toContain('kind:');
  });
});
