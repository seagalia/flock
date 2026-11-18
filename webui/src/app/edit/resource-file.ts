import { toJson } from '@bufbuild/protobuf';
import { stringify } from 'yaml';
import { DocKind, kindDocument } from '../builder/documents';
import { registry } from '../core/registry';
import { kindOf, Resource } from '../core/resources';
import { hasDocument } from './resource-doc';

// The resource files written with yaml, imported on the click that needs one so yaml stays out of the first bundle.

// resourceDocument gives the resource as a `kind:` document of a resource file, ready for `flock apply`. The UUID
// stays, so applying it targets this resource. Null for the kinds a resource file can't hold. It throws for an Any
// of a type the registry doesn't know, its JSON can't be written.
export function resourceDocument(resource: Resource): string | null {
  if (!hasDocument(resource)) {
    return null;
  }
  return kindDocument(kindOf(resource).name as DocKind, resource);
}

// resourceFile writes a resource as a file the cli reads back, a document of flock apply for the kinds a resource file
// holds and the manifest of create-<kind> for the others. Its first line is the command that takes it. It throws like
// resourceDocument for an Any the registry doesn't know.
export function resourceFile(resource: Resource): { name: string; text: string } {
  const kind = kindOf(resource);
  const name = `${resource.name || kind.name}.yml`;
  if (hasDocument(resource)) {
    return { name, text: `# flock apply -f ${name}\n${resourceDocument(resource)}` };
  }
  const manifest = toJson(kind.schema, resource, { useProtoFieldName: true, registry });
  return { name, text: `# flock create-${kind.name} -f ${name}\n${stringify(manifest)}` };
}
