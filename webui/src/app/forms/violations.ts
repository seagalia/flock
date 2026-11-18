import { Code, ConnectError } from '@connectrpc/connect';
import { FieldPath, ViolationsSchema } from '../../gen/buf/validate/validate_pb';
import { FieldError } from './rules';

// violationsOf reads the protovalidate violations the controller attaches to an InvalidArgument answer, so the
// forms show each one on the field it names
export function violationsOf(err: ConnectError): FieldError[] {
  if (err.code !== Code.InvalidArgument) {
    return [];
  }
  return err
    .findDetails(ViolationsSchema)
    .flatMap((details) => details.violations)
    .map((violation) => ({ path: pathOf(violation.field), message: violation.message }));
}

function pathOf(field: FieldPath | undefined): string {
  if (!field) {
    return '';
  }
  return field.elements
    .map((element) => {
      const subscript = element.subscript;
      switch (subscript.case) {
        case undefined:
          return element.fieldName;
        case 'stringKey':
          return `${element.fieldName}[${JSON.stringify(subscript.value)}]`;
        default:
          return `${element.fieldName}[${subscript.value}]`;
      }
    })
    .join('.');
}
