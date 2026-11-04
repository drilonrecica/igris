// Rewrites a .vsix so the same contents give the same bytes on any machine
// (decisions.md V19-P1, `make vsix-check`): vsce stamps its generated files
// with the current time and deflate output varies with the zlib in use, so
// every entry is re-stored uncompressed, sorted by name, with a fixed time
// and attributes. Node builtins only. Usage: node normalize-zip.mjs FILE
import { readFileSync, writeFileSync } from "node:fs";
import { crc32, inflateRawSync } from "node:zlib";

const file = process.argv[2];
if (!file) throw new Error("usage: node normalize-zip.mjs FILE.vsix");
const buf = readFileSync(file);

// End of central directory record.
let eocd = buf.length - 22;
while (eocd >= 0 && buf.readUInt32LE(eocd) !== 0x06054b50) eocd--;
if (eocd < 0) throw new Error(`${file}: not a zip`);
const count = buf.readUInt16LE(eocd + 10);
let p = buf.readUInt32LE(eocd + 16);

const entries = [];
for (let i = 0; i < count; i++) {
  if (buf.readUInt32LE(p) !== 0x02014b50) throw new Error(`${file}: bad central directory`);
  const method = buf.readUInt16LE(p + 10);
  const csize = buf.readUInt32LE(p + 20);
  const nlen = buf.readUInt16LE(p + 28);
  const xlen = buf.readUInt16LE(p + 30);
  const clen = buf.readUInt16LE(p + 32);
  const lho = buf.readUInt32LE(p + 42);
  const name = buf.toString("utf8", p + 46, p + 46 + nlen);
  const lnlen = buf.readUInt16LE(lho + 26);
  const lxlen = buf.readUInt16LE(lho + 28);
  const raw = buf.subarray(lho + 30 + lnlen + lxlen, lho + 30 + lnlen + lxlen + csize);
  const data = method === 8 ? inflateRawSync(raw) : raw;
  if (method !== 0 && method !== 8) throw new Error(`${file}: ${name}: unsupported method ${method}`);
  entries.push({ name, data });
  p += 46 + nlen + xlen + clen;
}
entries.sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));

const DOS_TIME = 0; // 00:00:00
const DOS_DATE = (0 << 9) | (1 << 5) | 1; // 1980-01-01
const parts = [];
const central = [];
let offset = 0;
for (const { name, data } of entries) {
  const nameBuf = Buffer.from(name, "utf8");
  const crc = crc32(data);
  const local = Buffer.alloc(30);
  local.writeUInt32LE(0x04034b50, 0);
  local.writeUInt16LE(20, 4); // version needed
  local.writeUInt16LE(0x0800, 6); // UTF-8 names
  local.writeUInt16LE(0, 8); // stored
  local.writeUInt16LE(DOS_TIME, 10);
  local.writeUInt16LE(DOS_DATE, 12);
  local.writeUInt32LE(crc, 14);
  local.writeUInt32LE(data.length, 18);
  local.writeUInt32LE(data.length, 22);
  local.writeUInt16LE(nameBuf.length, 26);
  parts.push(local, nameBuf, data);
  const cen = Buffer.alloc(46);
  cen.writeUInt32LE(0x02014b50, 0);
  cen.writeUInt16LE(0x031e, 4); // made by: Unix, 3.0
  cen.writeUInt16LE(20, 6);
  cen.writeUInt16LE(0x0800, 8);
  cen.writeUInt16LE(0, 10);
  cen.writeUInt16LE(DOS_TIME, 12);
  cen.writeUInt16LE(DOS_DATE, 14);
  cen.writeUInt32LE(crc, 16);
  cen.writeUInt32LE(data.length, 20);
  cen.writeUInt32LE(data.length, 24);
  cen.writeUInt16LE(nameBuf.length, 28);
  cen.writeUInt32LE(((name.endsWith("/") ? 0o40755 : 0o100644) << 16) >>> 0, 38);
  cen.writeUInt32LE(offset, 42);
  central.push(cen, nameBuf);
  offset += 30 + nameBuf.length + data.length;
}
const cdir = Buffer.concat(central);
const end = Buffer.alloc(22);
end.writeUInt32LE(0x06054b50, 0);
end.writeUInt16LE(entries.length, 8);
end.writeUInt16LE(entries.length, 10);
end.writeUInt32LE(cdir.length, 12);
end.writeUInt32LE(offset, 16);
writeFileSync(file, Buffer.concat([...parts, cdir, end]));
