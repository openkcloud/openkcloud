// 브라우저(WebCrypto)에서 RSA SSH 키페어 생성.
// 개인키는 호출자에게만 반환되고(로컬 다운로드용), 서버로는 공개키만 보낸다.
// 외부 라이브러리 없이 OpenSSH 공개키 포맷(ssh-rsa ...)과 PKCS8 PEM 개인키를 직접 인코딩한다.

// TS 5.7의 Uint8Array 제네릭(ArrayBuffer vs ArrayBufferLike) 혼선을 피하려 통일 별칭 사용
type Bytes = Uint8Array<ArrayBufferLike>;

function bytesToB64(bytes: Bytes): string {
  let s = "";
  for (let i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
  return btoa(s);
}

function b64urlToBytes(b64url: string): Bytes {
  const b64 = b64url.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(b64url.length / 4) * 4, "=");
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

function concat(...arrs: Bytes[]): Bytes {
  const len = arrs.reduce((n, a) => n + a.length, 0);
  const out = new Uint8Array(len);
  let o = 0;
  for (const a of arrs) { out.set(a, o); o += a.length; }
  return out;
}

// SSH wire 포맷: 4바이트 big-endian 길이 + 데이터
function sshString(data: Bytes): Bytes {
  const len = new Uint8Array(4);
  new DataView(len.buffer).setUint32(0, data.length, false);
  return concat(len, data);
}

// mpint: 선행 0 제거, 최상위 비트가 1이면 0x00 한 바이트 prepend(양수 표현)
function sshMpint(bytes: Bytes): Bytes {
  let start = 0;
  while (start < bytes.length - 1 && bytes[start] === 0) start++;
  let b: Bytes = bytes.slice(start);
  if (b.length > 0 && (b[0] & 0x80) !== 0) b = concat(new Uint8Array([0]), b);
  return sshString(b);
}

function pemWrap(label: string, der: Bytes): string {
  const b64 = bytesToB64(der);
  const lines = b64.match(/.{1,64}/g) ?? [];
  return `-----BEGIN ${label}-----\n${lines.join("\n")}\n-----END ${label}-----\n`;
}

export interface GeneratedKeypair {
  publicKey: string;      // OpenSSH authorized_keys 형식 (ssh-rsa AAAA... comment)
  privateKeyPem: string;  // PKCS8 PEM (ssh -i 로 사용)
}

/** RSA 3072 SSH 키페어를 브라우저에서 생성. 개인키는 서버로 보내지 말 것. */
export async function generateRsaSshKeypair(comment = ""): Promise<GeneratedKeypair> {
  const kp = await crypto.subtle.generateKey(
    {
      name: "RSASSA-PKCS1-v1_5",
      modulusLength: 3072,
      publicExponent: new Uint8Array([1, 0, 1]),
      hash: "SHA-256",
    },
    true,
    ["sign", "verify"],
  );
  const jwk = await crypto.subtle.exportKey("jwk", kp.publicKey);
  const e = b64urlToBytes(jwk.e as string);
  const n = b64urlToBytes(jwk.n as string);
  const blob = concat(sshString(new TextEncoder().encode("ssh-rsa")), sshMpint(e), sshMpint(n));
  const publicKey = `ssh-rsa ${bytesToB64(blob)}${comment ? " " + comment : ""}`;

  const pkcs8 = new Uint8Array(await crypto.subtle.exportKey("pkcs8", kp.privateKey));
  const privateKeyPem = pemWrap("PRIVATE KEY", pkcs8);

  return { publicKey, privateKeyPem };
}

/** 문자열을 파일로 다운로드(브라우저). */
export function downloadText(filename: string, content: string) {
  const blob = new Blob([content], { type: "application/octet-stream" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}
