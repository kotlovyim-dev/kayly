import assert from "node:assert/strict";
import { createHash, generateKeyPairSync, sign } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import { mkdtemp, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, before, beforeEach, describe, test } from "node:test";
import { assetName, cacheDir, checksum, ensureBinary } from "../lib/binary.js";

test("asset names follow the release workflow", () => {
  assert.equal(assetName("win32", "x64"), "kayly-windows-amd64.exe");
  assert.equal(assetName("win32", "arm64"), "kayly-windows-arm64.exe");
  assert.equal(assetName("darwin", "arm64"), "kayly-darwin-arm64");
  assert.equal(assetName("linux", "x64"), "kayly-linux-amd64");
  assert.throws(() => assetName("freebsd", "x64"), /no binary for freebsd\/x64/);
  assert.throws(() => assetName("linux", "ia32"), /no binary for linux\/ia32/);
});

test("checksum reads sha256sum output", () => {
  const sums = "AB12  kayly-linux-amd64\r\ncd34  *kayly-windows-amd64.exe\n";
  assert.equal(checksum(sums, "kayly-linux-amd64"), "ab12");
  assert.equal(checksum(sums, "kayly-windows-amd64.exe"), "cd34");
  assert.throws(() => checksum(sums, "kayly-darwin-arm64"), /no checksum for kayly-darwin-arm64/);
});

test("the cache is per user and per system", () => {
  assert.equal(cacheDir("win32", { LOCALAPPDATA: "C:\\L" }, "C:\\H"), join("C:\\L", "kayly"));
  assert.equal(cacheDir("darwin", {}, "/h"), join("/h", "Library", "Caches", "kayly"));
  assert.equal(cacheDir("linux", { XDG_CACHE_HOME: "/x" }, "/h"), join("/x", "kayly"));
  assert.equal(cacheDir("linux", {}, "/h"), join("/h", ".cache", "kayly"));
});

test("the npm package carries the release key", () => {
  const ours = readFileSync(new URL("../release.pub", import.meta.url), "utf8");
  const go = readFileSync(new URL("../../internal/upgrade/release.pub", import.meta.url), "utf8");
  assert.equal(ours.trim(), go.trim());
});

describe("ensureBinary", () => {
  const name = "kayly-linux-amd64";
  const bin = Buffer.from("#!/bin/sh\necho kayly\n");
  const { publicKey, privateKey } = generateKeyPairSync("ed25519");
  const key = publicKey.export({ type: "spki", format: "pem" });
  let files, hits, server, base, dir;

  const release = (binary = bin, signer = privateKey) => {
    const sums = Buffer.from(`${createHash("sha256").update(binary).digest("hex")}  ${name}\n`);
    return {
      "/v1.0.0/checksums.txt": sums,
      "/v1.0.0/checksums.txt.sig": sign(null, sums, signer),
      [`/v1.0.0/${name}`]: binary,
    };
  };
  const ensure = () => ensureBinary({ version: "v1.0.0", platform: "linux", arch: "x64", dir, key, base });

  before(async () => {
    server = createServer((req, res) => {
      hits++;
      const body = files[req.url];
      res.writeHead(body ? 200 : 404).end(body);
    });
    await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
    base = `http://127.0.0.1:${server.address().port}`;
  });
  after(() => server.close());
  beforeEach(async () => {
    hits = 0;
    dir = await mkdtemp(join(tmpdir(), "kayly-npm-"));
  });

  test("downloads a signed binary once", async (t) => {
    t.after(() => rm(dir, { recursive: true, force: true }));
    files = release();
    const file = await ensure();
    assert.equal(file, join(dir, "v1.0.0", name));
    assert.deepEqual(readFileSync(file), bin);
    assert.equal(hits, 3);
    assert.equal(await ensure(), file);
    assert.equal(hits, 3, "a cached binary is not downloaded again");
  });

  test("refuses a release signed by another key", async (t) => {
    t.after(() => rm(dir, { recursive: true, force: true }));
    files = release(bin, generateKeyPairSync("ed25519").privateKey);
    await assert.rejects(ensure(), /not signed by the release key/);
    assert.equal(existsSync(join(dir, "v1.0.0", name)), false);
  });

  test("refuses a binary that does not match its checksum", async (t) => {
    t.after(() => rm(dir, { recursive: true, force: true }));
    files = { ...release(), [`/v1.0.0/${name}`]: Buffer.from("tampered") };
    await assert.rejects(ensure(), /does not match its checksum/);
    assert.equal(existsSync(join(dir, "v1.0.0", name)), false);
  });

  test("says which release lacks this system's binary", async (t) => {
    t.after(() => rm(dir, { recursive: true, force: true }));
    files = release();
    await assert.rejects(
      ensureBinary({ version: "v1.0.0", platform: "win32", arch: "x64", dir, key, base }),
      /v1\.0\.0: no checksum for kayly-windows-amd64\.exe/,
    );
  });

  test("reports a missing release", async (t) => {
    t.after(() => rm(dir, { recursive: true, force: true }));
    files = {};
    await assert.rejects(ensure(), /checksums\.txt: 404/);
  });
});
