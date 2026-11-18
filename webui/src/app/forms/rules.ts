import {
  DescField,
  DescMessage,
  getOption,
  hasOption,
  isFieldSet,
  Message,
} from '@bufbuild/protobuf';
import { isScalarZeroValue } from '@bufbuild/protobuf/reflect';
import { isWrapperDesc } from '@bufbuild/protobuf/wkt';
import {
  FieldRules,
  Ignore,
  oneof as oneofRules,
  field as fieldRules,
  RepeatedRules,
  RepeatedRulesSchema,
  StringRules,
  StringRulesSchema,
} from '../../gen/buf/validate/validate_pb';

// A problem of one field, path in protovalidate's form (routes[0].backend_uuid), as the controller names them
export interface FieldError {
  path: string;
  message: string;
}

// The bounds every numeric rule message (int32, uint64, double, ...) shares
interface NumberBounds {
  lessThan: { case: 'lt' | 'lte'; value: number | bigint } | { case: undefined };
  greaterThan: { case: 'gt' | 'gte'; value: number | bigint } | { case: undefined };
}

const numberRuleCases = new Set([
  'int32',
  'int64',
  'uint32',
  'uint64',
  'sint32',
  'sint64',
  'fixed32',
  'fixed64',
  'sfixed32',
  'sfixed64',
  'float',
  'double',
]);

// The messages a form edits as one value, protovalidate doesn't look inside them either
const opaqueMessages = new Set([
  'google.protobuf.Any',
  'google.protobuf.Duration',
  'google.protobuf.Timestamp',
  'google.protobuf.Struct',
]);

export interface RulesOptions {
  // A submit leaves required out because the controller gives its defaults (refresh_rate, health check timings)
  // before it validates, and the browser can't tell which fields it fills.
  skipRequired?: boolean;
}

// rulesProblems checks the protovalidate rules simple enough for the browser, which are required, string lengths, ip
// and uri, numeric bounds, min_items and unique. The controller checks every rule again and stays the authority.
export function rulesProblems(
  schema: DescMessage,
  value: Message,
  prefix = '',
  options: RulesOptions = {},
): FieldError[] {
  const fields = value as unknown as Record<string, unknown>;
  const problems: FieldError[] = [];
  for (const member of schema.members) {
    if (member.kind === 'oneof') {
      const selected = fields[member.localName] as { case: string | undefined; value: unknown };
      const required = hasOption(member, oneofRules) && getOption(member, oneofRules).required;
      if (required && !options.skipRequired && selected.case === undefined) {
        problems.push({
          path: prefix + member.name,
          message: 'exactly one field is required in oneof',
        });
      }
      const field = member.fields.find((f) => f.localName === selected.case);
      if (field) {
        problems.push(...fieldProblems(field, selected.value, prefix + field.name, options));
      }
      continue;
    }
    if (member.oneof) {
      continue;
    }
    problems.push(
      ...fieldProblems(member, fields[member.localName], prefix + member.name, options),
    );
  }
  return problems;
}

function fieldProblems(
  field: DescField,
  value: unknown,
  path: string,
  options: RulesOptions,
): FieldError[] {
  const rules = hasOption(field, fieldRules) ? getOption(field, fieldRules) : undefined;
  if (rules?.ignore === Ignore.ALWAYS) {
    return [];
  }
  const skipZero = rules?.ignore === Ignore.IF_ZERO_VALUE && !populated(field, value);
  if (skipZero) {
    return [];
  }
  if (rules?.required && !options.skipRequired && !populated(field, value)) {
    return [{ path, message: 'value is required' }];
  }
  const own = rules ? ruleProblem(field, rules, value) : null;
  if (own) {
    return [{ path, message: own }];
  }
  return nestedProblems(field, value, path, options);
}

function nestedProblems(
  field: DescField,
  value: unknown,
  path: string,
  options: RulesOptions,
): FieldError[] {
  const isPlainMessage = (desc: DescMessage) =>
    !opaqueMessages.has(desc.typeName) && !isWrapperDesc(desc);
  if (field.fieldKind === 'message' && value !== undefined && isPlainMessage(field.message)) {
    return rulesProblems(field.message, value as Message, path + '.', options);
  }
  if (field.fieldKind === 'list' && field.listKind === 'message' && isPlainMessage(field.message)) {
    return (value as Message[]).flatMap((item, i) =>
      rulesProblems(field.message, item, `${path}[${i}].`, options),
    );
  }
  return [];
}

// populated follows protovalidate's notion of a set field, so that required and ignore judge a field the way
// the controller will
function populated(field: DescField, value: unknown): boolean {
  switch (field.fieldKind) {
    case 'message':
      return value !== undefined;
    case 'list':
      return (value as unknown[]).length > 0;
    case 'map':
      return Object.keys(value as object).length > 0;
    case 'enum':
      return value !== undefined && value !== 0;
    case 'scalar':
      return value !== undefined && !isScalarZeroValue(field.scalar, value);
  }
}

function ruleProblem(field: DescField, rules: FieldRules, value: unknown): string | null {
  // a field with presence left unset, a wrapper included, has nothing for its type rules to check
  if (value === undefined) {
    return null;
  }
  const typed = rules.type;
  if (typed.case === 'string') {
    return stringProblem(typed.value, value as string);
  }
  if (typed.case === 'repeated') {
    return repeatedProblem(field, typed.value, value as unknown[]);
  }
  if (typed.case !== undefined && numberRuleCases.has(typed.case)) {
    return numberProblem(typed.value as unknown as NumberBounds, value as number | bigint);
  }
  return null;
}

function stringProblem(rules: StringRules, value: string): string | null {
  const length = [...value].length;
  if (isFieldSet(rules, StringRulesSchema.field.minLen) && length < rules.minLen) {
    return `value length must be at least ${rules.minLen} characters`;
  }
  if (isFieldSet(rules, StringRulesSchema.field.maxLen) && length > rules.maxLen) {
    return `value length must be at most ${rules.maxLen} characters`;
  }
  if (rules.wellKnown.case === 'ip' && rules.wellKnown.value && !isIp(value)) {
    return 'value must be a valid IP address';
  }
  if (rules.wellKnown.case === 'uri' && rules.wellKnown.value && !isUri(value)) {
    return 'value must be a valid URI';
  }
  return null;
}

function repeatedProblem(field: DescField, rules: RepeatedRules, items: unknown[]): string | null {
  if (isFieldSet(rules, RepeatedRulesSchema.field.minItems) && items.length < rules.minItems) {
    return `value must contain at least ${rules.minItems} item(s)`;
  }
  // unique applies to scalars and enums only, messages have no simple equality
  const comparable = field.fieldKind === 'list' && field.listKind !== 'message';
  if (rules.unique && comparable && new Set(items.map(String)).size !== items.length) {
    return 'repeated value must contain unique items';
  }
  return null;
}

function numberProblem(rules: NumberBounds, value: number | bigint): string | null {
  const above = rules.greaterThan;
  const below = rules.lessThan;
  const tooLow =
    (above.case === 'gt' && !(value > above.value)) ||
    (above.case === 'gte' && !(value >= above.value));
  const tooHigh =
    (below.case === 'lt' && !(value < below.value)) ||
    (below.case === 'lte' && !(value <= below.value));
  if (!tooLow && !tooHigh) {
    return null;
  }
  const bounds: string[] = [];
  if (above.case === 'gt') {
    bounds.push(`greater than ${above.value}`);
  }
  if (above.case === 'gte') {
    bounds.push(`greater than or equal to ${above.value}`);
  }
  if (below.case === 'lt') {
    bounds.push(`less than ${below.value}`);
  }
  if (below.case === 'lte') {
    bounds.push(`less than or equal to ${below.value}`);
  }
  return `value must be ${bounds.join(' and ')}`;
}

const ipv4 = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/;

function isIp(value: string): boolean {
  if (ipv4.test(value)) {
    return true;
  }
  // the URL parser of the browser knows every IPv6 notation, a bracketed host only parses when it is one
  return value.includes(':') && URL.canParse(`http://[${value}]/`);
}

function isUri(value: string): boolean {
  // an absolute URI has a scheme, which URL requires too
  return URL.canParse(value);
}
