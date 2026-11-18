import { clone, DescField, DescMessage, isFieldSet, Message } from '@bufbuild/protobuf';
import { Any, AnySchema, anyUnpack } from '@bufbuild/protobuf/wkt';
import { registry } from '../core/registry';
import type { DocKind } from '../builder/documents';
import { kindOf, Resource } from '../core/resources';

const flockMetadataPrefix = 'flock.';
const documentKinds: string[] = ['vs', 'backend', 'vip'] satisfies DocKind[];

export function hasDocument(resource: Resource): boolean {
  return documentKinds.includes(kindOf(resource).name);
}

// clonePrefill is what Clone hands to Create. The flock.* keys describe the original's own state (its lock, its
// provisioner, the secret holding its key), so the copy loses them all.
export function clonePrefill(resource: Resource): Resource {
  const copy = clone(kindOf(resource).schema, resource) as Resource & {
    metadata?: Record<string, string>;
  };
  copy.UUID = '';
  copy.name = '';
  const metadata = copy.metadata ?? {};
  for (const key of Object.keys(metadata)) {
    if (key.startsWith(flockMetadataPrefix)) {
      delete metadata[key];
    }
  }
  return copy;
}

// holdsUnknownAny tells whether an Any of the message, however deep, can't be decoded with the registry, so its
// JSON (and a resource file document) can't be written
export function holdsUnknownAny(schema: DescMessage, message: Message): boolean {
  if (schema.typeName === AnySchema.typeName) {
    return !decodes(message as Any);
  }
  // the other well-known types hold no Any
  if (!schema.typeName.startsWith('flock.')) {
    return false;
  }
  return schema.fields.some((field) => isFieldSet(message, field) && fieldHolds(field, message));
}

function fieldHolds(field: DescField, message: Message): boolean {
  const fields = message as unknown as Record<string, unknown>;
  const value = field.oneof
    ? (fields[field.oneof.localName] as { value: unknown }).value
    : fields[field.localName];
  switch (field.fieldKind) {
    case 'message':
      return holdsUnknownAny(field.message, value as Message);
    case 'list':
      return (
        field.listKind === 'message' &&
        (value as Message[]).some((item) => holdsUnknownAny(field.message, item))
      );
    case 'map':
      return (
        field.mapKind === 'message' &&
        Object.values(value as Record<string, Message>).some((item) =>
          holdsUnknownAny(field.message, item),
        )
      );
    default:
      return false;
  }
}

function decodes(any: Any): boolean {
  if (any.typeUrl === '') {
    return true;
  }
  try {
    return anyUnpack(any, registry) !== undefined;
  } catch {
    return false;
  }
}
