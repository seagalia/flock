import { certificateChainDetails, certificateInfo, expiryLabel } from './certificate-details';

// an EC certificate: CN=flock.test, O=Seagalia, valid until 2036-10-18
const pem = `-----BEGIN CERTIFICATE-----
MIIBgzCCASqgAwIBAgIUbNT4SHMH0XO7cwEw0n5m4ljOcEowCgYIKoZIzj0EAwIw
KDETMBEGA1UEAwwKZmxvY2sudGVzdDERMA8GA1UECgwIU2VhZ2FsaWEwHhcNMjYx
MDIxMTI1OTIxWhcNMzYxMDE4MTI1OTIxWjAoMRMwEQYDVQQDDApmbG9jay50ZXN0
MREwDwYDVQQKDAhTZWFnYWxpYTBZMBMGByqGSM49AgEGCCqGSM49AwEHA0IABAqp
1bgD3ArJMmPu5KvIkpwOzK4oezrkwIybyvWHwZjEshmAg7Phsct/0DnKGoXIHSsc
KfN9Ew+xXNyTGThZ+eGjMjAwMB0GA1UdDgQWBBRK/OA5JY+yGW+179oakWuXpouz
TDAPBgNVHRMBAf8EBTADAQH/MAoGCCqGSM49BAMCA0cAMEQCIDD1dVUJF2ZjiDeS
AQJxrP+N193BE6Ibcw37IA50VzWrAiBgyIPIAEidlgErZICfZ+akjxF+ET8+Ai7M
GCgfIFglkQ==
-----END CERTIFICATE-----`;

// web.flock.test (RSA 2048, SANs, serial 0xa1b2c3) signed by Flock Test CA (EC P-256, self-signed)
const chain = `-----BEGIN CERTIFICATE-----
MIICnjCCAkWgAwIBAgIEAKGywzAKBggqhkjOPQQDAjArMRYwFAYDVQQDDA1GbG9j
ayBUZXN0IENBMREwDwYDVQQKDAhTZWFnYWxpYTAeFw0yNjEwMjExNDQ0MzlaFw0y
NzEwMjExNDQ0MzlaMD8xFzAVBgNVBAMMDndlYi5mbG9jay50ZXN0MREwDwYDVQQK
DAhTZWFnYWxpYTERMA8GA1UECwwIUGxhdGZvcm0wggEiMA0GCSqGSIb3DQEBAQUA
A4IBDwAwggEKAoIBAQC5gVpBvcnF6MQ93Zu/kiZG13ushjMm0iwx0lECXL66Xqkp
VkeMj9ARNmQ4AyHpmlIx2eLUNba1DMgrLnYamd/LiGZ9fD0NA+Byn8zM9XKZYwAK
KmyVJJ6HgoGdYVZobcjqLlz6cyEj8+dIoTEvQ8aZ/GA4pgn6VyOvNzDQo/UPwopf
Rd/Amx95fXVEpAbQETLmCpjLLf/SIt4QI31Dqy4w0Qe6FAV9iqkWIesp60wc29A2
kOKRUTCm+ZylId4HSlOFOr0aySnZBLkSKNc9nlFh5zewxN16uCq9LAYTOurBo1O/
Z0Y7mi8BElpffbUoM7quPNnF4cJoKxWCr5cS3daXAgMBAAGjeDB2MD0GA1UdEQQ2
MDSCDndlYi5mbG9jay50ZXN0ggwqLmZsb2NrLnRlc3SHBAoAAAGBDm9wc0BmbG9j
ay50ZXN0MAkGA1UdEwQCMAAwCwYDVR0PBAQDAgWgMB0GA1UdJQQWMBQGCCsGAQUF
BwMBBggrBgEFBQcDAjAKBggqhkjOPQQDAgNHADBEAiA6nj6uTApx5sgiNnySE3Sb
wpZRJOyozgdbYxD5zYMK9AIgMntMjQ70VnYYOhXyVGK4Nw/EBAMnp7B2cGwgErsa
6bk=
-----END CERTIFICATE-----
-----BEGIN CERTIFICATE-----
MIIBnTCCAUOgAwIBAgIUGNR7zPkhJx0EfGF6iGMtJ9rZ2pwwCgYIKoZIzj0EAwIw
KzEWMBQGA1UEAwwNRmxvY2sgVGVzdCBDQTERMA8GA1UECgwIU2VhZ2FsaWEwHhcN
MjYxMDIxMTQ0NDM5WhcNMzYxMDE4MTQ0NDM5WjArMRYwFAYDVQQDDA1GbG9jayBU
ZXN0IENBMREwDwYDVQQKDAhTZWFnYWxpYTBZMBMGByqGSM49AgEGCCqGSM49AwEH
A0IABHwdbPGfKd9wOvU1IrGQHcu4jrjoY+8rhYOV4tx4+vRDLU1r1ZaR2sYb4eXj
fKoIj+x2avZTeOOoqFhMuri4tPWjRTBDMB0GA1UdDgQWBBTqC5Orq49LBsRHO1uD
ky1ZZYA/OzASBgNVHRMBAf8ECDAGAQH/AgEAMA4GA1UdDwEB/wQEAwIBBjAKBggq
hkjOPQQDAgNIADBFAiEA0NSpjeZCzitDEBz3s6kX6AoR0Ykbi88l3LoqB92gtvkC
ICmpEcinvVVezoBiDSTPWXaR547xo+bo6TVNUo8Jzx8K
-----END CERTIFICATE-----`;

describe('certificate details', () => {
  it('reads every certificate of the chain, the server one first', async () => {
    const [leaf, ca] = await certificateChainDetails(chain);

    expect(leaf.subject).toBe('CN=web.flock.test, O=Seagalia, OU=Platform');
    expect(leaf.issuer).toBe('CN=Flock Test CA, O=Seagalia');
    expect(leaf.selfSigned).toBe(false);
    expect(leaf.isCa).toBe(false);
    expect(leaf.dnsNames).toEqual(['web.flock.test', '*.flock.test']);
    expect(leaf.ipAddresses).toEqual(['10.0.0.1']);
    expect(leaf.emails).toEqual(['ops@flock.test']);
    expect(leaf.serialNumber).toBe('A1:B2:C3');
    expect(leaf.publicKey).toBe('RSA 2048 bits');
    expect(leaf.signatureAlgorithm).toBe('ECDSA with SHA-256');
    expect(leaf.keyUsages).toEqual(['digitalSignature', 'keyEncipherment']);
    expect(leaf.extendedKeyUsages).toEqual(['serverAuth', 'clientAuth']);
    expect(leaf.notAfter.toISOString()).toBe('2027-10-21T14:44:39.000Z');
    expect(leaf.sha256Fingerprint).toBe(
      'D6:B4:EF:05:72:87:0F:97:55:95:8C:09:4D:75:2C:B3:3D:A5:61:B5:3C:1B:7C:4E:AF:98:F7:1F:FC:D6:C8:F5',
    );

    expect(ca.selfSigned).toBe(true);
    expect(ca.isCa).toBe(true);
    expect(ca.pathLength).toBe(0);
    expect(ca.publicKey).toBe('EC P-256');
    expect(ca.keyUsages).toEqual(['crlSign', 'keyCertSign']);
  });

  it('leaves out what is not a readable certificate', async () => {
    const broken = '-----BEGIN CERTIFICATE-----\nbm90IGEgY2VydA==\n-----END CERTIFICATE-----';

    expect(await certificateChainDetails(broken)).toEqual([]);
    expect(await certificateChainDetails('not a pem')).toEqual([]);
  });

  it('reads the first certificate of a chain', () => {
    const info = certificateInfo(pem + '\n' + pem);
    expect(info?.commonName).toBe('flock.test');
    expect(info?.organisation).toBe('Seagalia');
    expect(info?.validTo.toISOString()).toBe('2036-10-18T12:59:21.000Z');
    expect(certificateInfo('not a certificate')).toBeNull();
  });

  it('tells in whole days when a certificate expires', () => {
    const now = new Date('2026-10-21T12:00:00Z');
    const at = (iso: string) => new Date(iso);

    expect(expiryLabel(at('2026-11-02T13:00:00Z'), now)).toBe('expires in 12 days');
    expect(expiryLabel(at('2026-10-22T12:00:00Z'), now)).toBe('expires in 1 day');
    expect(expiryLabel(at('2026-10-21T18:00:00Z'), now)).toBe('expires today');
    expect(expiryLabel(at('2026-10-21T06:00:00Z'), now)).toBe('expired today');
    expect(expiryLabel(at('2026-10-18T12:00:00Z'), now)).toBe('expired 3 days ago');
  });
});
