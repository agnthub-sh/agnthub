'use strict';

// The agnthub npm launcher: downloads the native agnthub binary for this platform from
// the public GitHub release matching the package version, verifies it against the
// SHA-256 checksums embedded in this package at publish time, caches it and runs it.
// Node standard library only; no install scripts.

const crypto = require('node:crypto');
const fs = require('node:fs');
const http = require('node:http');
const https = require('node:https');
const os = require('node:os');
const path = require('node:path');
const zlib = require('node:zlib');
const { spawn } = require('node:child_process');

const REPOSITORY = 'agnthub-sh/agnthub';
const MAX_ARCHIVE_BYTES = 256 * 1024 * 1024;
const MAX_REDIRECTS = 5;
const DOWNLOAD_TIMEOUT_MS = 120 * 1000;

const PLATFORMS = { linux: 'linux', darwin: 'darwin', win32: 'windows' };
const ARCHES = { x64: 'amd64', arm64: 'arm64' };

/** An error the launcher reports as-is, without a stack trace. */
class LauncherError extends Error {}

/** The release target for a Node platform/arch pair. */
function target(platform, arch) {
  const goos = PLATFORMS[platform];
  const goarch = ARCHES[arch];
  if (!goos || !goarch) {
    throw new LauncherError(
      `no agnthub release binary for ${platform}/${arch}; supported: linux, darwin and win32 on x64 or arm64`,
    );
  }
  const windows = goos === 'windows';
  return {
    goos,
    goarch,
    archive: windows ? 'zip' : 'tar.gz',
    binary: windows ? 'agnthub.exe' : 'agnthub',
  };
}

function assetName(version, t) {
  return `agnthub_${version}_${t.goos}_${t.goarch}.${t.archive}`;
}

/**
 * Where release archives are downloaded from. AGNTHUB_RELEASE_BASE_URL points at a
 * mirror (used by tests); checksums are still the ones embedded in this package.
 */
function releaseBaseURL(env, version) {
  const base = env.AGNTHUB_RELEASE_BASE_URL || `https://github.com/${REPOSITORY}/releases/download/v${version}`;
  return base.replace(/\/+$/, '');
}

function cacheRoot(env, platform) {
  if (env.AGNTHUB_CACHE_DIR) {
    return env.AGNTHUB_CACHE_DIR;
  }
  const home = os.homedir();
  if (platform === 'win32') {
    return path.join(env.LOCALAPPDATA || path.join(home, 'AppData', 'Local'), 'agnthub', 'Cache');
  }
  if (platform === 'darwin') {
    return path.join(home, 'Library', 'Caches', 'agnthub');
  }
  return path.join(env.XDG_CACHE_HOME || path.join(home, '.cache'), 'agnthub');
}

function sha256(buf) {
  return crypto.createHash('sha256').update(buf).digest('hex');
}

function verify(archive, name, expected) {
  const actual = sha256(archive);
  if (actual !== expected) {
    throw new LauncherError(
      `refusing to run ${name}: its SHA-256 is ${actual} but this package expects ${expected}; ` +
        'the download is corrupt or does not belong to this release',
    );
  }
}

function download(url, userAgent, redirects = MAX_REDIRECTS) {
  return new Promise((resolve, reject) => {
    const client = url.startsWith('https:') ? https : url.startsWith('http:') ? http : null;
    if (!client) {
      reject(new LauncherError(`cannot download ${url}: only http(s) release URLs are supported`));
      return;
    }
    const fail = (reason) => reject(new LauncherError(`cannot download ${url}: ${reason}`));
    const req = client.get(url, { headers: { 'user-agent': userAgent } }, (res) => {
      const status = res.statusCode;
      if (status >= 300 && status < 400 && res.headers.location) {
        res.resume();
        if (redirects === 0) {
          fail('too many redirects');
          return;
        }
        resolve(download(new URL(res.headers.location, url).href, userAgent, redirects - 1));
        return;
      }
      if (status !== 200) {
        res.resume();
        fail(`HTTP ${status}`);
        return;
      }
      const chunks = [];
      let size = 0;
      res.on('data', (chunk) => {
        size += chunk.length;
        if (size > MAX_ARCHIVE_BYTES) {
          req.destroy(new Error('archive is unexpectedly large'));
          return;
        }
        chunks.push(chunk);
      });
      res.on('end', () => resolve(Buffer.concat(chunks)));
      res.on('error', (err) => fail(err.message));
    });
    req.setTimeout(DOWNLOAD_TIMEOUT_MS, () => req.destroy(new Error('timed out')));
    req.on('error', (err) => fail(err.message));
  });
}

function cString(buf, start, length) {
  const field = buf.subarray(start, start + length);
  const end = field.indexOf(0);
  return field.toString('utf8', 0, end === -1 ? field.length : end);
}

function paxPath(body) {
  let found = null;
  let offset = 0;
  while (offset < body.length) {
    const space = body.indexOf(0x20, offset);
    if (space === -1) {
      break;
    }
    const length = Number.parseInt(body.toString('utf8', offset, space), 10);
    if (!Number.isInteger(length) || length <= 0) {
      break;
    }
    const record = body.toString('utf8', space + 1, offset + length - 1);
    const eq = record.indexOf('=');
    if (record.slice(0, eq) === 'path') {
      found = record.slice(eq + 1);
    }
    offset += length;
  }
  return found;
}

/** Returns the regular file named wanted from a gzip-compressed tar archive. */
function extractTar(archive, wanted) {
  const tar = zlib.gunzipSync(archive);
  let offset = 0;
  let longName = null;
  while (offset + 512 <= tar.length) {
    const header = tar.subarray(offset, offset + 512);
    if (header.every((b) => b === 0)) {
      break;
    }
    let name = cString(header, 0, 100);
    if (cString(header, 257, 5) === 'ustar') {
      const prefix = cString(header, 345, 155);
      if (prefix) {
        name = `${prefix}/${name}`;
      }
    }
    const size = Number.parseInt(cString(header, 124, 12).trim() || '0', 8);
    const type = String.fromCharCode(header[156]);
    const start = offset + 512;
    if (!Number.isInteger(size) || size < 0 || start + size > tar.length) {
      throw new LauncherError('release archive is truncated');
    }
    const body = tar.subarray(start, start + size);
    offset = start + Math.ceil(size / 512) * 512;
    if (type === 'x') {
      longName = paxPath(body) ?? longName;
      continue;
    }
    if (type === 'L') {
      longName = cString(body, 0, body.length);
      continue;
    }
    if (type === 'g') {
      continue;
    }
    const entry = longName ?? name;
    longName = null;
    if ((type === '0' || type === '\0') && entry === wanted) {
      return Buffer.from(body);
    }
  }
  throw new LauncherError(`release archive does not contain ${wanted}`);
}

/** Returns the file named wanted from a zip archive (stored or deflated entries). */
function extractZip(zip, wanted) {
  let eocd = -1;
  for (let i = zip.length - 22; i >= Math.max(0, zip.length - 22 - 0xffff); i--) {
    if (zip.readUInt32LE(i) === 0x06054b50) {
      eocd = i;
      break;
    }
  }
  if (eocd === -1) {
    throw new LauncherError('release archive is not a zip file');
  }
  const count = zip.readUInt16LE(eocd + 10);
  let p = zip.readUInt32LE(eocd + 16);
  for (let n = 0; n < count; n++) {
    if (zip.readUInt32LE(p) !== 0x02014b50) {
      throw new LauncherError('release archive has a corrupt zip directory');
    }
    const method = zip.readUInt16LE(p + 10);
    const compressedSize = zip.readUInt32LE(p + 20);
    const size = zip.readUInt32LE(p + 24);
    const nameLength = zip.readUInt16LE(p + 28);
    const extraLength = zip.readUInt16LE(p + 30);
    const commentLength = zip.readUInt16LE(p + 32);
    const local = zip.readUInt32LE(p + 42);
    const name = zip.toString('utf8', p + 46, p + 46 + nameLength);
    p += 46 + nameLength + extraLength + commentLength;
    if (name !== wanted) {
      continue;
    }
    if (zip.readUInt32LE(local) !== 0x04034b50) {
      throw new LauncherError('release archive has a corrupt zip entry');
    }
    const start = local + 30 + zip.readUInt16LE(local + 26) + zip.readUInt16LE(local + 28);
    const data = zip.subarray(start, start + compressedSize);
    let out;
    if (method === 0) {
      out = Buffer.from(data);
    } else if (method === 8) {
      out = zlib.inflateRawSync(data);
    } else {
      throw new LauncherError(`release archive uses unsupported zip compression method ${method}`);
    }
    if (out.length !== size) {
      throw new LauncherError('release archive has a corrupt zip entry');
    }
    return out;
  }
  throw new LauncherError(`release archive does not contain ${wanted}`);
}

function extract(archive, t) {
  try {
    return t.archive === 'zip' ? extractZip(archive, t.binary) : extractTar(archive, t.binary);
  } catch (err) {
    if (err instanceof LauncherError) {
      throw err;
    }
    throw new LauncherError(`cannot extract the release archive: ${err.message}`);
  }
}

/**
 * Returns the path of the verified native binary for this platform, downloading,
 * verifying and caching it on first use. Nothing unverified is ever written to the cache.
 */
async function ensureBinary({ version, checksums, env, platform, arch }) {
  const t = target(platform, arch);
  const name = assetName(version, t);
  const expected = checksums[name];
  if (!expected) {
    throw new LauncherError(`this package carries no checksum for ${name}`);
  }
  const dir = path.join(cacheRoot(env, platform), version, `${t.goos}_${t.goarch}`);
  const binary = path.join(dir, t.binary);
  if (fs.existsSync(binary)) {
    return binary;
  }

  const archive = await download(`${releaseBaseURL(env, version)}/${name}`, `agnthub-npm/${version}`);
  verify(archive, name, expected);
  const contents = extract(archive, t);

  // Stage in a sibling directory and rename it into place so a concurrent or
  // interrupted launcher never sees a partial binary.
  const parent = path.dirname(dir);
  fs.mkdirSync(parent, { recursive: true });
  const staging = fs.mkdtempSync(path.join(parent, `.${t.goos}_${t.goarch}-`));
  try {
    fs.writeFileSync(path.join(staging, t.binary), contents, { mode: 0o755 });
    try {
      fs.renameSync(staging, dir);
    } catch (err) {
      if (!fs.existsSync(binary)) {
        throw err;
      }
    }
  } finally {
    fs.rmSync(staging, { recursive: true, force: true });
  }
  return binary;
}

/** Runs binary with args on the inherited stdio and resolves to its exit status. */
function run(binary, args) {
  return new Promise((resolve, reject) => {
    const child = spawn(binary, args, { stdio: 'inherit' });
    // Ctrl-C reaches the whole foreground process group: let the CLI handle it and
    // report its own status. Termination aimed at the launcher is passed on.
    const ignore = () => {};
    const forward = (signal) => child.kill(signal);
    process.on('SIGINT', ignore);
    process.on('SIGTERM', forward);
    process.on('SIGHUP', forward);
    const cleanup = () => {
      process.off('SIGINT', ignore);
      process.off('SIGTERM', forward);
      process.off('SIGHUP', forward);
    };
    child.on('error', (err) => {
      cleanup();
      reject(new LauncherError(`cannot run ${binary}: ${err.message}`));
    });
    child.on('exit', (code, signal) => {
      cleanup();
      resolve({ code, signal });
    });
  });
}

/** The launcher entry point: resolves the binary (or AGNTHUB_BINARY) and runs it. */
async function main(args, env = process.env) {
  const pkg = require('../package.json');
  const embedded = require('../checksums.json');
  let binary = env.AGNTHUB_BINARY;
  if (!binary) {
    if (embedded.version !== pkg.version) {
      throw new LauncherError(
        `this package (${pkg.version}) carries checksums for ${embedded.version}; reinstall agnthub`,
      );
    }
    binary = await ensureBinary({
      version: pkg.version,
      checksums: embedded.sha256,
      env,
      platform: process.platform,
      arch: process.arch,
    });
  }
  return run(binary, args);
}

module.exports = {
  LauncherError,
  assetName,
  cacheRoot,
  ensureBinary,
  extractTar,
  extractZip,
  main,
  target,
};
