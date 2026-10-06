#!/usr/bin/env node
// Launcher for the Spicrawl CLI (the spicrawl command) when installed from npm.
//
// This one package carries a prebuilt Go binary for every supported platform
// under bin/<platform>-<arch>/spicrawl[.exe] (npm's process.platform and
// process.arch names). This script picks the one for the running machine and
// runs it with the same argv, stdio and exit code. There is no postinstall
// step and nothing is downloaded at install time.
//
// SPICRAWL_BINARY=/path/to/spicrawl overrides the lookup (SPICRAWL_BINARY is
// still honoured as a fallback).
"use strict";

const { spawn } = require("child_process");
const fs = require("fs");
const path = require("path");

const INSTALL_DOCS = "https://docs.spicrawl.com/cli/install";

function binaryPath() {
  if (process.env.SPICRAWL_BINARY) return process.env.SPICRAWL_BINARY;
  if (process.env.SPICRAWL_BINARY) return process.env.SPICRAWL_BINARY;

  const target = `${process.platform}-${process.arch}`;
  const exe = process.platform === "win32" ? "spicrawl.exe" : "spicrawl";
  const bin = path.join(__dirname, target, exe);
  if (!fs.existsSync(bin)) {
    process.stderr.write(
      `spicrawl: no prebuilt binary for ${target}.\n` +
        "Supported: darwin-arm64, darwin-x64, linux-arm64, linux-x64, win32-arm64, win32-x64.\n" +
        `Other install options: ${INSTALL_DOCS}\n`
    );
    process.exit(1);
  }
  // Some package managers drop the executable bit; restore it (best-effort).
  if (process.platform !== "win32") {
    try {
      fs.accessSync(bin, fs.constants.X_OK);
    } catch (_) {
      try {
        fs.chmodSync(bin, 0o755);
      } catch (_) {}
    }
  }
  return bin;
}

const bin = binaryPath();
const child = spawn(bin, process.argv.slice(2), { stdio: "inherit" });

// Forward termination signals so an agent that kills `npx @spicrawl/cli` stops the
// real process too. The exit handler below then re-raises the child's signal (or
// returns its code), so this process ends the way the child did.
for (const sig of ["SIGINT", "SIGTERM", "SIGHUP"]) {
  process.on(sig, () => child.kill(sig));
}

child.on("error", (err) => {
  process.stderr.write(`spicrawl: failed to run ${path.basename(bin)}: ${err.message}\n`);
  process.exit(1);
});

child.on("exit", (code, signal) => {
  if (signal) {
    process.removeAllListeners(signal);
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code === null ? 1 : code);
});
