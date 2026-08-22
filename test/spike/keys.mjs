// The extension id Chrome assigns to an unpacked extension normally depends on where it was
// unpacked, which would make a host manifest's allowed_origins machine-specific. A `key` in
// the extension manifest pins it instead, and the id can be derived from that key offline —
// this is the same derivation `take5 install` will need.
//
// Verified against Chrome 151: the id computed here is the id Chrome loaded (SPIKE.md §1.3).

import { generateKeyPairSync, createHash } from "node:crypto";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const KEY_FILE = join(here, "key.json");

// sha256 of the DER SPKI, first 16 bytes, hex digits mapped onto a–p.
export function extensionIdFromKey(base64Der) {
  return createHash("sha256")
    .update(Buffer.from(base64Der, "base64"))
    .digest("hex")
    .slice(0, 32)
    .replace(/[0-9a-f]/g, (c) => String.fromCharCode(97 + parseInt(c, 16)));
}

// Generated on first use rather than committed: the spike extensions are throwaway, and a
// private key in the repository would be a liability for nothing.
export function ensureKey() {
  if (existsSync(KEY_FILE)) return JSON.parse(readFileSync(KEY_FILE, "utf8"));

  const { publicKey, privateKey } = generateKeyPairSync("rsa", {
    modulusLength: 2048,
    publicKeyEncoding: { type: "spki", format: "der" },
    privateKeyEncoding: { type: "pkcs8", format: "pem" },
  });
  const key = publicKey.toString("base64");
  const pair = { key, id: extensionIdFromKey(key) };
  writeFileSync(KEY_FILE, JSON.stringify(pair, null, 2));
  writeFileSync(join(here, "key.pem"), privateKey);
  return pair;
}
