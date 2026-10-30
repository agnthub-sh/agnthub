#!/usr/bin/env node
'use strict';

const { LauncherError, main } = require('../lib/launcher.js');

main(process.argv.slice(2)).then(
  ({ code, signal }) => {
    if (signal) {
      process.kill(process.pid, signal);
      return;
    }
    process.exitCode = code ?? 1;
  },
  (err) => {
    const message = err instanceof LauncherError ? err.message : err.stack || String(err);
    process.stderr.write(`agnthub (npm launcher): error: ${message}\n`);
    process.exitCode = 1;
  },
);
