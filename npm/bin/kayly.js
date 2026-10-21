#!/usr/bin/env node
// Runs the kayly binary of this package's version with the given arguments.
import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { cacheDir, ensureBinary } from "../lib/binary.js";

const pkg = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8"));
const key = readFileSync(new URL("../release.pub", import.meta.url));

let bin;
try {
  bin = await ensureBinary({
    version: `v${pkg.version}`,
    platform: process.platform,
    arch: process.arch,
    dir: cacheDir(),
    key,
    onDownload: (what) => console.error(`Downloading ${what}…`),
  });
} catch (err) {
  console.error(`error: ${err.message}`);
  process.exit(1);
}

const run = spawnSync(bin, process.argv.slice(2), { stdio: "inherit" });
if (run.error) {
  console.error(`error: run ${bin}: ${run.error.message}`);
  process.exit(1);
}
if (run.signal) process.kill(process.pid, run.signal);
process.exit(run.status ?? 1);
