import assert from "node:assert/strict";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";

import { publishNpmRelease } from "./publish-npm-release.mjs";

test("publishes missing native packages before the umbrella and skips completed work", async (t) => {
  const workspace = await mkdtemp(path.join(tmpdir(), "tokenholic-publish-test-"));
  t.after(() => rm(workspace, { recursive: true, force: true }));
  for (const directory of ["darwin-arm64", "darwin-x64", "cli"]) {
    const packageRoot = path.join(workspace, "packages", directory);
    await mkdir(packageRoot, { recursive: true });
    await writeFile(path.join(packageRoot, "package.json"), JSON.stringify({
      name: `@tokenholic/${directory}`,
      version: "1.2.3",
    }));
  }

  const calls = [];
  const runner = (_command, args) => {
    calls.push(args);
    if (args[0] === "view" && args[1] === "@tokenholic/darwin-arm64@1.2.3") {
      return { status: 0, stdout: '"1.2.3"', stderr: "" };
    }
    if (args[0] === "view") {
      return { status: 1, stdout: "", stderr: "E404 not found" };
    }
    return { status: 0, stdout: "", stderr: "" };
  };
  let output = "";
  await publishNpmRelease(workspace, runner, { write: (value) => (output += value) });

  const publishes = calls.filter((args) => args[0] === "publish");
  assert.match(publishes[0][1], /darwin-x64$/);
  assert.match(publishes[1][1], /cli$/);
  assert.match(output, /already published @tokenholic\/darwin-arm64@1.2.3/);
});
