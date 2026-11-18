const refPrefix = 'secret://';

export interface SecretRef {
  secretUuid: string;
  key: string;
}

// parseSecretRef reads a metadata value of the form secret://<secret uuid>/<key>. A value with the prefix but no
// uuid or no key, or a slash in the key, is no reference: the dashboard shows it as text and the controller refuses
// it on save. This follows ParseRef of sdk/flock/api/config/secret/v1.
export function parseSecretRef(value: string): SecretRef | null {
  if (!value.startsWith(refPrefix)) {
    return null;
  }
  const rest = value.slice(refPrefix.length);
  const slash = rest.indexOf('/');
  if (slash < 0) {
    return null;
  }
  const secretUuid = rest.slice(0, slash);
  const key = rest.slice(slash + 1);
  const isWellFormed = secretUuid !== '' && key !== '' && !key.includes('/');
  return isWellFormed ? { secretUuid, key } : null;
}

export function secretRef(secretUuid: string, key: string): string {
  return `${refPrefix}${secretUuid}/${key}`;
}
