#!/usr/bin/env node
'use strict';

// Release step (`make release`): embeds the SHA-256 of every release archive from a
// GoReleaser dist/ directory into checksums.json and sets the package version, so the
// published launcher verifies downloads against checksums that ship with the package.
//
// usage: node npm/agnthub/scripts/embed-checksums.js <dist-dir>

const fs = require('node:fs');
const path = require('node:path');
const { assetName, target } = require('../lib/launcher.js');

const TARGETS = [
  ['linux', 'x64'],
  ['linux', 'arm64'],
  ['darwin', 'x64'],
  ['darwin', 'arm64'],
  ['win32', 'x64'],
  ['win32', 'arm64'],
];

function fail(message) {
  process.stderr.write(`embed-checksums: ${message}\n`);
  process.exit(1);
}

const dist = process.argv[2];
if (!dist) {
  fail('usage: embed-checksums.js <dist-dir>');
}
const { version } = JSON.parse(fs.readFileSync(path.join(dist, 'metadata.json'), 'utf8'));
if (!/^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/.test(version)) {
  fail(`dist/ holds version ${version}; build a tagged release (vX.Y.Z), not a snapshot`);
}

const published = new Map();
for (const line of fs.readFileSync(path.join(dist, 'SHA256SUMS'), 'utf8').split('\n')) {
  const match = /^([0-9a-f]{64}) {2}(\S+)$/.exec(line);
  if (match) {
    published.set(match[2], match[1]);
  }
}

const sha256 = {};
for (const [platform, arch] of TARGETS) {
  const name = assetName(version, target(platform, arch));
  const sum = published.get(name);
  if (!sum) {
    fail(`SHA256SUMS has no entry for ${name}`);
  }
  sha256[name] = sum;
}

const root = path.join(__dirname, '..');
fs.writeFileSync(path.join(root, 'checksums.json'), `${JSON.stringify({ version, sha256 }, null, 2)}\n`);
const manifestPath = path.join(root, 'package.json');
const manifest = JSON.parse(fs.readFileSync(manifestPath, 'utf8'));
manifest.version = version;
fs.writeFileSync(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
process.stdout.write(`embedded ${TARGETS.length} checksums for agnthub ${version}\n`);
