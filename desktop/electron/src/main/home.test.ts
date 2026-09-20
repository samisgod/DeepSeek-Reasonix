import assert from "node:assert/strict";
import { sep } from "node:path";
import { test } from "node:test";
import { reasonixHome } from "./home.js";

const unix = (env: NodeJS.ProcessEnv, platform: NodeJS.Platform = "darwin") =>
  reasonixHome({ env, platform, homedir: () => "/home/fallback", cwd: () => "/work" });

test("REASONIX_HOME wins, with ~ and ${VAR} expansion and relative paths made absolute", () => {
  assert.equal(unix({ REASONIX_HOME: "/data/rx", HOME: "/home/u" }), "/data/rx");
  assert.equal(unix({ REASONIX_HOME: "  /data/rx/  ", HOME: "/home/u" }), "/data/rx");
  assert.equal(unix({ REASONIX_HOME: "~/rx", HOME: "/home/u" }), "/home/u/rx");
  assert.equal(unix({ REASONIX_HOME: "~", HOME: "/home/u" }), "/home/u");
  assert.equal(unix({ REASONIX_HOME: "${BASE}/rx", BASE: "/srv", HOME: "/home/u" }), "/srv/rx");
  assert.equal(unix({ REASONIX_HOME: "${MISSING:-/opt}/rx", HOME: "/home/u" }), "/opt/rx");
  assert.equal(unix({ REASONIX_HOME: "rel/rx", HOME: "/home/u" }), "/work/rel/rx");
});

test("macOS and Linux default to ~/.reasonix from $HOME, then the passwd home", () => {
  assert.equal(unix({ HOME: "/home/u" }), "/home/u/.reasonix");
  assert.equal(unix({ HOME: "/home/u" }, "linux"), "/home/u/.reasonix");
  assert.equal(unix({}), "/home/fallback/.reasonix");
});

test("Windows uses %APPDATA%\\reasonix with the Roaming fallback", () => {
  const win = (env: NodeJS.ProcessEnv) => reasonixHome({ env, platform: "win32", homedir: () => "", cwd: () => "C:\\work" });
  assert.equal(win({ APPDATA: "C:\\Users\\u\\AppData\\Roaming" }), ["C:\\Users\\u\\AppData\\Roaming", "reasonix"].join(sep));
  assert.equal(win({ USERPROFILE: "C:\\Users\\u" }), ["C:\\Users\\u", "AppData", "Roaming", "reasonix"].join(sep));
});

// The Go service resolves portable mode from its own executable directory
// (internal/config/portable.go); this mirrors it so the hello data homes match.
const PROGRAM_DIR = "/opt/reasonix/versions/v1.39.0";

const portable = (env: NodeJS.ProcessEnv, marker: boolean, programDir = PROGRAM_DIR) =>
  reasonixHome({ env, platform: "linux", homedir: () => "/home/fallback", cwd: () => "/work", programDir, fileExists: () => marker });

test("portable mode keeps the data folder beside the Go binary", () => {
  const dataDir = `${PROGRAM_DIR}/reasonix-data`;
  assert.equal(portable({ HOME: "/home/u" }, false), "/home/u/.reasonix");
  assert.equal(portable({ HOME: "/home/u" }, true), dataDir);
  // An explicit on needs no marker; an explicit off ignores one.
  assert.equal(portable({ HOME: "/home/u", REASONIX_PORTABLE: "on" }, false), dataDir);
  assert.equal(portable({ HOME: "/home/u", REASONIX_PORTABLE: "off" }, true), "/home/u/.reasonix");
  // Unset and "auto" fall back to the marker.
  assert.equal(portable({ HOME: "/home/u", REASONIX_PORTABLE: "" }, false), "/home/u/.reasonix");
  assert.equal(portable({ HOME: "/home/u", REASONIX_PORTABLE: "auto" }, true), dataDir);
});

test("REASONIX_PORTABLE_DIR relocates the portable data folder", () => {
  assert.equal(
    portable({ HOME: "/home/u", REASONIX_PORTABLE: "on", REASONIX_PORTABLE_DIR: "/media/usb/rx" }, false),
    "/media/usb/rx",
  );
});

test("REASONIX_HOME still wins over portable mode", () => {
  assert.equal(portable({ HOME: "/home/u", REASONIX_HOME: "/data/rx", REASONIX_PORTABLE: "on" }, true), "/data/rx");
});

test("portable mode is skipped when the program directory is unresolvable", () => {
  const resolved = reasonixHome({ env: { HOME: "/home/u", REASONIX_PORTABLE: "on" }, platform: "linux", homedir: () => "", cwd: () => "/work" });
  assert.equal(resolved, "/home/u/.reasonix");
});
