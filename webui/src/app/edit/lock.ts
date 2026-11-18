export const lockMetadataKey = 'flock.autoprovision';

export const lockReason =
  'Managed automatically (metadata flock.autoprovision). Change it where it was provisioned.';

// The controller never checks the lock, the dashboard enforces it on its own
export function isLocked(resource: { metadata?: Record<string, string> }): boolean {
  return resource.metadata?.[lockMetadataKey] === 'true';
}
