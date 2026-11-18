import { AsnConvert } from '@peculiar/asn1-schema';
import {
  BasicConstraints,
  Certificate as X509Certificate,
  ExtendedKeyUsage,
  id_ce_basicConstraints,
  id_ce_extKeyUsage,
  id_ce_keyUsage,
  id_ce_subjectAltName,
  KeyUsage,
  Name,
  SubjectAlternativeName,
} from '@peculiar/asn1-x509';

export interface CertificateDetails {
  subject: string;
  issuer: string;
  selfSigned: boolean;
  isCa: boolean;
  // undefined when the CA sets no limit
  pathLength?: number;
  notBefore: Date;
  notAfter: Date;
  dnsNames: string[];
  ipAddresses: string[];
  emails: string[];
  uris: string[];
  serialNumber: string;
  publicKey: string;
  signatureAlgorithm: string;
  keyUsages: string[];
  extendedKeyUsages: string[];
  sha256Fingerprint: string;
}

const pemCertificates = /-----BEGIN CERTIFICATE-----([\s\S]+?)-----END CERTIFICATE-----/g;

const attributeNames: Record<string, string> = {
  '2.5.4.3': 'CN',
  '2.5.4.5': 'serialNumber',
  '2.5.4.6': 'C',
  '2.5.4.7': 'L',
  '2.5.4.8': 'ST',
  '2.5.4.10': 'O',
  '2.5.4.11': 'OU',
  '1.2.840.113549.1.9.1': 'emailAddress',
  '0.9.2342.19200300.100.1.25': 'DC',
};

const algorithmNames: Record<string, string> = {
  '1.2.840.113549.1.1.1': 'RSA',
  '1.2.840.10045.2.1': 'EC',
  '1.3.101.112': 'Ed25519',
  '1.3.101.113': 'Ed448',
  '1.2.840.113549.1.1.5': 'RSA with SHA-1',
  '1.2.840.113549.1.1.10': 'RSA-PSS',
  '1.2.840.113549.1.1.11': 'RSA with SHA-256',
  '1.2.840.113549.1.1.12': 'RSA with SHA-384',
  '1.2.840.113549.1.1.13': 'RSA with SHA-512',
  '1.2.840.10045.4.3.2': 'ECDSA with SHA-256',
  '1.2.840.10045.4.3.3': 'ECDSA with SHA-384',
  '1.2.840.10045.4.3.4': 'ECDSA with SHA-512',
};

// the named curve of an EC key, as the DER of its object identifier
const curveNames: Record<string, string> = {
  '06082a8648ce3d030107': 'P-256',
  '06052b81040022': 'P-384',
  '06052b81040023': 'P-521',
};

const extendedKeyUsageNames: Record<string, string> = {
  '1.3.6.1.5.5.7.3.1': 'serverAuth',
  '1.3.6.1.5.5.7.3.2': 'clientAuth',
  '1.3.6.1.5.5.7.3.3': 'codeSigning',
  '1.3.6.1.5.5.7.3.4': 'emailProtection',
  '1.3.6.1.5.5.7.3.8': 'timeStamping',
  '1.3.6.1.5.5.7.3.9': 'OCSPSigning',
};

function hex(bytes: ArrayBuffer | Uint8Array, separator = ''): string {
  return Array.from(new Uint8Array(bytes), (byte) => byte.toString(16).padStart(2, '0')).join(
    separator,
  );
}

function formatName(name: Name): string {
  return name
    .flatMap((rdn) =>
      rdn.map((attribute) => {
        const label = attributeNames[attribute.type] ?? attribute.type;
        return `${label}=${attribute.value.toString()}`;
      }),
    )
    .join(', ');
}

function serialNumber(serial: ArrayBuffer): string {
  const bytes = new Uint8Array(serial);
  // DER keeps a leading zero byte so that the integer stays positive, it is not part of the serial
  const significant = bytes.length > 1 && bytes[0] === 0 ? bytes.slice(1) : bytes;
  return hex(significant, ':').toUpperCase();
}

// publicKeyLabel gives the key type with its size or curve, the RSA size needs WebCrypto to read the modulus
async function publicKeyLabel(cert: X509Certificate): Promise<string> {
  const keyInfo = cert.tbsCertificate.subjectPublicKeyInfo;
  const algorithm = keyInfo.algorithm.algorithm;
  const name = algorithmNames[algorithm] ?? algorithm;
  if (name === 'EC') {
    const curve = keyInfo.algorithm.parameters ? curveNames[hex(keyInfo.algorithm.parameters)] : '';
    return curve ? `EC ${curve}` : 'EC';
  }
  if (name !== 'RSA') {
    return name;
  }
  try {
    const spki = AsnConvert.serialize(keyInfo);
    const key = await crypto.subtle.importKey(
      'spki',
      spki,
      { name: 'RSASSA-PKCS1-v1_5', hash: 'SHA-256' },
      true,
      ['verify'],
    );
    return `RSA ${(key.algorithm as RsaHashedKeyAlgorithm).modulusLength} bits`;
  } catch {
    return 'RSA';
  }
}

async function details(der: Uint8Array<ArrayBuffer>): Promise<CertificateDetails> {
  const cert = AsnConvert.parse(der, X509Certificate);
  const tbs = cert.tbsCertificate;
  const subject = formatName(tbs.subject);
  const issuer = formatName(tbs.issuer);
  const found: CertificateDetails = {
    subject,
    issuer,
    selfSigned: subject === issuer,
    isCa: false,
    notBefore: tbs.validity.notBefore.getTime(),
    notAfter: tbs.validity.notAfter.getTime(),
    dnsNames: [],
    ipAddresses: [],
    emails: [],
    uris: [],
    serialNumber: serialNumber(tbs.serialNumber),
    publicKey: await publicKeyLabel(cert),
    signatureAlgorithm:
      algorithmNames[cert.signatureAlgorithm.algorithm] ?? cert.signatureAlgorithm.algorithm,
    keyUsages: [],
    extendedKeyUsages: [],
    sha256Fingerprint: hex(await crypto.subtle.digest('SHA-256', der), ':').toUpperCase(),
  };
  for (const extension of tbs.extensions ?? []) {
    const value = extension.extnValue.buffer;
    switch (extension.extnID) {
      case id_ce_subjectAltName:
        for (const altName of AsnConvert.parse(value, SubjectAlternativeName)) {
          addIfSet(found.dnsNames, altName.dNSName);
          addIfSet(found.ipAddresses, altName.iPAddress);
          addIfSet(found.emails, altName.rfc822Name);
          addIfSet(found.uris, altName.uniformResourceIdentifier);
        }
        break;
      case id_ce_basicConstraints: {
        const constraints = AsnConvert.parse(value, BasicConstraints);
        found.isCa = constraints.cA;
        found.pathLength = constraints.pathLenConstraint;
        break;
      }
      case id_ce_keyUsage:
        found.keyUsages = AsnConvert.parse(value, KeyUsage).toJSON();
        break;
      case id_ce_extKeyUsage:
        found.extendedKeyUsages = AsnConvert.parse(value, ExtendedKeyUsage).map(
          (oid) => extendedKeyUsageNames[oid] ?? oid,
        );
        break;
    }
  }
  return found;
}

function addIfSet(list: string[], value: string | undefined) {
  if (value) {
    list.push(value);
  }
}

// certificateChainDetails reads every certificate of a PEM chain, the server one first.
// A certificate that cannot be read is left out, the chain stays shown as text.
export async function certificateChainDetails(certChain: string): Promise<CertificateDetails[]> {
  const chain: CertificateDetails[] = [];
  for (const pem of certChain.matchAll(pemCertificates)) {
    try {
      const der = Uint8Array.from(atob(pem[1].replace(/\s/g, '')), (char) => char.charCodeAt(0));
      chain.push(await details(der));
    } catch {
      continue;
    }
  }
  return chain;
}

export interface CertificateInfo {
  commonName: string;
  organisation: string;
  validTo: Date;
}

const dayMs = 24 * 3600 * 1000;

// expiryLabel tells in whole days how long a certificate stays valid, or since when it is not
export function expiryLabel(validTo: Date, now: Date): string {
  const remainingMs = validTo.getTime() - now.getTime();
  const days = Math.floor(Math.abs(remainingMs) / dayMs);
  const dayWord = days === 1 ? 'day' : 'days';
  if (remainingMs < 0) {
    return days === 0 ? 'expired today' : `expired ${days} ${dayWord} ago`;
  }
  return days === 0 ? 'expires today' : `expires in ${days} ${dayWord}`;
}

const oidCommonName = '2.5.4.3';
const oidOrganisation = '2.5.4.10';
const pemCertificate = /-----BEGIN CERTIFICATE-----([\s\S]+?)-----END CERTIFICATE-----/;

// certificateInfo reads the first certificate of a PEM chain, the one of the server
export function certificateInfo(certChain: string): CertificateInfo | null {
  const pem = pemCertificate.exec(certChain);
  if (!pem) {
    return null;
  }
  try {
    const der = Uint8Array.from(atob(pem[1].replace(/\s/g, '')), (char) => char.charCodeAt(0));
    const cert = AsnConvert.parse(der, X509Certificate);
    const subject = cert.tbsCertificate.subject;
    return {
      commonName: nameField(subject, oidCommonName) || 'unknown',
      organisation: nameField(subject, oidOrganisation) || 'unknown',
      validTo: cert.tbsCertificate.validity.notAfter.getTime(),
    };
  } catch {
    return null;
  }
}

function nameField(name: Name, oid: string): string {
  for (const rdn of name) {
    for (const attribute of rdn) {
      if (attribute.type === oid) {
        return attribute.value.toString();
      }
    }
  }
  return '';
}
