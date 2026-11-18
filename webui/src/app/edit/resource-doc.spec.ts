import { create } from '@bufbuild/protobuf';
import { VIPSchema } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { clonePrefill } from './resource-doc';

describe('clonePrefill', () => {
  it('keeps everything but the name, the UUID and the lock key', () => {
    const vip = create(VIPSchema, {
      UUID: 'u1',
      name: 'front',
      ip: '10.0.0.1',
      metadata: { team: 'lb', 'flock.autoprovision': 'true' },
    });

    const copy = clonePrefill(vip) as typeof vip;

    expect(copy).toMatchObject({ UUID: '', name: '', ip: '10.0.0.1', metadata: { team: 'lb' } });
  });

  it('drops every flock metadata key', () => {
    const vip = create(VIPSchema, {
      name: 'front',
      metadata: {
        team: 'lb',
        'flock.provision.by': 'agent-1',
        'flock.acme.secret.ref': 'secret://flock-acme-1',
        'flock.acme.failures': '2',
      },
    });

    const copy = clonePrefill(vip) as typeof vip;

    expect(copy.metadata).toEqual({ team: 'lb' });
  });
});
