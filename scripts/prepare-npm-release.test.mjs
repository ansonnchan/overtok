import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { chmod, mkdir, mkdtemp, readFile, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import test from "node:test";

import { parseChecksums, prepareNpmRelease } from "./prepare-npm-release.mjs";

test("parses strict checksum manifests", () => {
  const hash = "a".repeat(64);
  assert.equal(parseChecksums(`${hash}  archive.tar.gz\n`).get("archive.tar.gz"), hash);
  assert.throws(() => parseChecksums(`${hash} archive.tar.gz\n`), /invalid or duplicate/);
  assert.throws(() => parseChecksums(`${hash}  archive.tar.gz\n${hash}  archive.tar.gz\n`), /invalid or duplicate/);
});

test("rejects invalid release versions before filesystem changes", async () => {
  await assert.rejects(prepareNpmRelease("v1.2.3", "missing", "missing"), /semantic version/);
});

test("prepares exact platform and umbrella packages", async (t) => {
  const root = await mkdtemp(path.join(tmpdir(), "overtok-npm-test-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const artifacts = path.join(root, "artifacts");
  const workspace = path.join(root, "workspace");
  await mkdir(artifacts, { recursive: true });
  const sums = [];
  for (const arch of ["arm64", "x64"]) {
    const input = path.join(root, `input-${arch}`);
    await mkdir(input);
    await writeFile(path.join(input, "overtok"), `synthetic-${arch}`);
    await chmod(path.join(input, "overtok"), 0o755);
    await writeFile(path.join(input, "ccusage.lock.json"), "synthetic-lock");
    const name = `overtok_1.2.3_darwin_${arch}.tar.gz`;
    const archive = path.join(artifacts, name);
    const packed = spawnSync("tar", ["-czf", archive, "-C", input, "overtok", "ccusage.lock.json"], { shell: false });
    assert.equal(packed.status, 0);
    const hash = createHash("sha256").update(await readFile(archive)).digest("hex");
    sums.push(`${hash}  ${name}`);
  }
  await writeFile(path.join(artifacts, "SHA256SUMS"), `${sums.join("\n")}\n`);

  for (const packageName of ["darwin-arm64", "darwin-x64"]) {
    const directory = path.join(workspace, "packages", packageName);
    await mkdir(directory, { recursive: true });
    await writeFile(path.join(directory, "package.json"), JSON.stringify({ name: `@overtok/${packageName}`, version: "development" }));
  }
  const cliDirectory = path.join(workspace, "packages", "cli");
  await mkdir(cliDirectory, { recursive: true });
  await writeFile(path.join(cliDirectory, "package.json"), JSON.stringify({
    name: "@overtok/cli",
    version: "development",
    optionalDependencies: { "@overtok/darwin-x64": "workspace:*", "@overtok/darwin-arm64": "workspace:*" },
  }));

  await prepareNpmRelease("1.2.3", artifacts, workspace);
  const umbrella = JSON.parse(await readFile(path.join(cliDirectory, "package.json"), "utf8"));
  assert.equal(umbrella.version, "1.2.3");
  assert.deepEqual(umbrella.optionalDependencies, {
    "@overtok/darwin-arm64": "1.2.3",
    "@overtok/darwin-x64": "1.2.3",
  });
  const binary = path.join(workspace, "packages", "darwin-arm64", "bin", "overtok");
  assert.equal(await readFile(binary, "utf8"), "synthetic-arm64");
  assert.notEqual((await stat(binary)).mode & 0o111, 0);
});
