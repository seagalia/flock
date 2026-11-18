import { parseSecretRef, secretRef } from './secret-refs';

describe('secret references', () => {
  const uuid = '5f1e1bb7-2f6c-43c1-a048-c77e48e9cbe5';

  it('parses a reference into the secret uuid and the key', () => {
    expect(parseSecretRef(`secret://${uuid}/token`)).toEqual({ secretUuid: uuid, key: 'token' });
    expect(parseSecretRef(`secret://${uuid}/a.b_c-1`)).toEqual({
      secretUuid: uuid,
      key: 'a.b_c-1',
    });
  });

  it.each([
    ['a plain value', 'token'],
    ['the prefix alone', 'secret://'],
    ['no key', `secret://${uuid}`],
    ['an empty key', `secret://${uuid}/`],
    ['an empty uuid', 'secret:///token'],
    ['a slash in the key', `secret://${uuid}/a/b`],
    ['another prefix', `Secret://${uuid}/token`],
    ['a prefix not at the start', ` secret://${uuid}/token`],
  ])('gives null for %s', (_, value) => {
    expect(parseSecretRef(value)).toBeNull();
  });

  it('writes a reference that parses back', () => {
    const written = secretRef(uuid, 'token');
    expect(written).toBe(`secret://${uuid}/token`);
    expect(parseSecretRef(written)).toEqual({ secretUuid: uuid, key: 'token' });
  });
});
