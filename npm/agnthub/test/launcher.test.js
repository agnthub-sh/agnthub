'use strict';

const assert = require('node:assert/strict');
const crypto = require('node:crypto');
const fs = require('node:fs');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const { after, before, describe, it } = require('node:test');
const zlib = require('node:zlib');

const { LauncherError, assetName, ensureBinary, extractZip, target } = require('../lib/launcher.js');

const VERSION = '1.2.3';
const BINARY = Buffer.from('#!/bin/sh\necho native agnthub "$@"\n');

function tarGz(name, data) {
  const header = Buffer.alloc(512);
  header.write(name, 0);
  header.write('0000755\0', 100);
  header.write('0000000\0', 108);
  header.write('0000000\0', 116);
  header.write(`${data.length.toString(8).padStart(11, '0')}\0`, 124);
  header.write('00000000000\0', 136);
  header.write('0', 156);
  header.write('ustar\0', 257);
  header.write('00', 263);
  header.fill(0x20, 148, 156);
  let sum = 0;
  for (const b of header) {
    sum += b;
  }
  header.write(`${sum.toString(8).padStart(6, '0')}\0 `, 148);
  const padding = Buffer.alloc((512 - (data.length % 512)) % 512);
  return zlib.gzipSync(Buffer.concat([header, data, padding, Buffer.alloc(1024)]));
}

// A deflated zip entry written with a data descriptor, the layout Go's archive/zip uses.
function zip(name, data) {
  const nameBytes = Buffer.from(name);
  const compressed = zlib.deflateRawSync(data);
  const crc = zlib.crc32 ? zlib.crc32(data) : 0;
  const local = Buffer.alloc(30);
  local.writeUInt32LE(0x04034b50, 0);
  local.writeUInt16LE(20, 4);
  local.writeUInt16LE(nameBytes.length, 26);
  local.writeUInt16LE(0x0008, 6);
  local.writeUInt16LE(8, 8);
  const descriptor = Buffer.alloc(16);
  descriptor.writeUInt32LE(0x08074b50, 0);
  descriptor.writeUInt32LE(crc, 4);
  descriptor.writeUInt32LE(compressed.length, 8);
  descriptor.writeUInt32LE(data.length, 12);
  const central = Buffer.alloc(46);
  central.writeUInt32LE(0x02014b50, 0);
  central.writeUInt16LE(20, 4);
  central.writeUInt16LE(20, 6);
  central.writeUInt16LE(0x0008, 8);
  central.writeUInt16LE(8, 10);
  central.writeUInt32LE(crc, 16);
  central.writeUInt32LE(compressed.length, 20);
  central.writeUInt32LE(data.length, 24);
  central.writeUInt16LE(nameBytes.length, 28);
  const entry = Buffer.concat([local, nameBytes, compressed, descriptor]);
  const directory = Buffer.concat([central, nameBytes]);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0);
  end.writeUInt16LE(1, 8);
  end.writeUInt16LE(1, 10);
  end.writeUInt32LE(directory.length, 12);
  end.writeUInt32LE(entry.length, 16);
  return Buffer.concat([entry, directory, end]);
}

const sha256 = (buf) => crypto.createHash('sha256').update(buf).digest('hex');

describe('ensureBinary', () => {
  const platform = 'linux';
  const arch = 'x64';
  const name = assetName(VERSION, target(platform, arch));
  const good = tarGz('agnthub', BINARY);
  const served = new Map();
  let server;
  let baseURL;

  before(async () => {
    server = http.createServer((req, res) => {
      const body = served.get(req.url);
      if (!body) {
        res.writeHead(404).end();
        return;
      }
      res.writeHead(200).end(body);
    });
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    baseURL = `http://127.0.0.1:${server.address().port}`;
  });
  after(() => server.close());

  const cacheDir = () => fs.mkdtempSync(path.join(os.tmpdir(), 'agnthub-launcher-'));

  it('installs the verified binary for the platform and reuses the cache', async () => {
    served.set(`/good/${name}`, good);
    const env = { AGNTHUB_RELEASE_BASE_URL: `${baseURL}/good/`, AGNTHUB_CACHE_DIR: cacheDir() };
    const checksums = { [name]: sha256(good) };
    const binary = await ensureBinary({ version: VERSION, checksums, env, platform, arch });
    assert.deepEqual(fs.readFileSync(binary), BINARY);
    assert.equal(fs.statSync(binary).mode & 0o100, 0o100, 'the owner can execute the cached binary');

    served.delete(`/good/${name}`);
    assert.equal(await ensureBinary({ version: VERSION, checksums, env, platform, arch }), binary);
  });

  it('rejects an archive whose checksum differs and caches nothing', async () => {
    const corrupt = Buffer.from(good);
    corrupt[corrupt.length - 10] ^= 0xff;
    served.set(`/corrupt/${name}`, corrupt);
    const env = { AGNTHUB_RELEASE_BASE_URL: `${baseURL}/corrupt`, AGNTHUB_CACHE_DIR: cacheDir() };
    await assert.rejects(
      ensureBinary({ version: VERSION, checksums: { [name]: sha256(good) }, env, platform, arch }),
      (err) => err instanceof LauncherError && err.message.includes(sha256(good)),
    );
    assert.deepEqual(fs.readdirSync(env.AGNTHUB_CACHE_DIR, { recursive: true }), []);
  });

  it('refuses a platform without an embedded checksum', async () => {
    const env = { AGNTHUB_RELEASE_BASE_URL: `${baseURL}/good`, AGNTHUB_CACHE_DIR: cacheDir() };
    await assert.rejects(
      ensureBinary({ version: VERSION, checksums: {}, env, platform, arch }),
      LauncherError,
    );
  });

  it('reports a missing release asset', async () => {
    const env = { AGNTHUB_RELEASE_BASE_URL: `${baseURL}/missing`, AGNTHUB_CACHE_DIR: cacheDir() };
    await assert.rejects(
      ensureBinary({ version: VERSION, checksums: { [name]: sha256(good) }, env, platform, arch }),
      LauncherError,
    );
  });
});

describe('target', () => {
  it('selects the archive for each supported platform', () => {
    assert.equal(assetName(VERSION, target('linux', 'arm64')), 'agnthub_1.2.3_linux_arm64.tar.gz');
    assert.equal(assetName(VERSION, target('darwin', 'x64')), 'agnthub_1.2.3_darwin_amd64.tar.gz');
    assert.equal(assetName(VERSION, target('win32', 'arm64')), 'agnthub_1.2.3_windows_arm64.zip');
    assert.equal(target('win32', 'x64').binary, 'agnthub.exe');
  });

  it('refuses unsupported platforms', () => {
    assert.throws(() => target('freebsd', 'x64'), LauncherError);
    assert.throws(() => target('linux', 'ia32'), LauncherError);
  });
});

describe('extractZip', () => {
  it('reads a deflated entry written with a data descriptor', () => {
    assert.deepEqual(extractZip(zip('agnthub.exe', BINARY), 'agnthub.exe'), BINARY);
  });

  it('refuses an archive without the binary', () => {
    assert.throws(() => extractZip(zip('other.exe', BINARY), 'agnthub.exe'), LauncherError);
  });
});
