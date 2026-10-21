// The npm package carries no binary. On first run it downloads the release's kayly binary for
// this system and uses it only when checksums.txt carries a valid ed25519 signature by the
// release key (release.pub, the key internal/upgrade and install.sh use) and the binary
// matches its line there.
import { createHash, createPublicKey, verify } from "node:crypto";
import { existsSync } from "node:fs";
import { mkdir, rename, rm, writeFile } from "node:fs/promises";
import { homedir } from "node:os";
import { dirname, join } from "node:path";

export const releases = "https://github.com/kotlovyim-dev/kayly/releases/download";

const systems = { win32: "windows", darwin: "darwin", linux: "linux" };
const arches = { x64: "amd64", arm64: "arm64" };

// assetName is the release file for a Node platform and arch.
export function assetName(platform, arch) {
  const os = systems[platform];
  const goarch = arches[arch];
  if (!os || !goarch) {
    throw new Error(`Kayly publishes no binary for ${platform}/${arch}; build it from source: https://github.com/kotlovyim-dev/kayly`);
  }
  return `kayly-${os}-${goarch}${os === "windows" ? ".exe" : ""}`;
}

// checksum finds name's hash in sha256sum output.
export function checksum(sums, name) {
  for (const line of sums.split("\n")) {
    const at = line.indexOf("  ");
    if (at > 0 && line.slice(at + 2).trim().replace(/^\*/, "") === name) {
      return line.slice(0, at).toLowerCase();
    }
  }
  throw new Error(`no checksum for ${name}`);
}

// cacheDir is where downloaded binaries are kept, per user.
export function cacheDir(platform = process.platform, env = process.env, home = homedir()) {
  if (platform === "win32") return join(env.LOCALAPPDATA || join(home, "AppData", "Local"), "kayly");
  if (platform === "darwin") return join(home, "Library", "Caches", "kayly");
  return join(env.XDG_CACHE_HOME || join(home, ".cache"), "kayly");
}

// ensureBinary returns the path of version's binary in dir, downloading and verifying it on
// first use. onDownload is called before a download starts.
export async function ensureBinary({ version, platform, arch, dir, key, base = releases, onDownload = () => {} }) {
  const name = assetName(platform, arch);
  const file = join(dir, version, name);
  if (existsSync(file)) return file;

  onDownload(`${version}/${name}`);
  const url = `${base}/${version}/`;
  const sums = await get(url + "checksums.txt");
  const sig = await get(url + "checksums.txt.sig");
  if (!verify(null, sums, createPublicKey(key), sig)) {
    throw new Error(`${version}: checksums.txt is not signed by the release key`);
  }
  let want;
  try {
    want = checksum(sums.toString("utf8"), name);
  } catch (err) {
    throw new Error(`${version}: ${err.message}`, { cause: err });
  }
  const bin = await get(url + name);
  if (createHash("sha256").update(bin).digest("hex") !== want) {
    throw new Error(`${version}: ${name} does not match its checksum`);
  }

  await mkdir(dirname(file), { recursive: true });
  const tmp = `${file}.${process.pid}.tmp`;
  await writeFile(tmp, bin, { mode: 0o755 });
  try {
    await rename(tmp, file);
  } catch (err) {
    // Another run got there first; on Windows its binary may be running and so cannot be replaced.
    await rm(tmp, { force: true });
    if (!existsSync(file)) throw new Error(`install ${file}: ${err.message}`, { cause: err });
  }
  return file;
}

async function get(url) {
  let res;
  try {
    res = await fetch(url);
  } catch (err) {
    throw new Error(`GET ${url}: ${err.message}`, { cause: err });
  }
  if (!res.ok) throw new Error(`GET ${url}: ${res.status} ${res.statusText}`);
  return Buffer.from(await res.arrayBuffer());
}
