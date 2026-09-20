import { existsSync } from "node:fs";
import { isAbsolute, join, normalize, resolve, sep } from "node:path";

export interface HomeEnvironment {
  env: NodeJS.ProcessEnv;
  platform: NodeJS.Platform;
  homedir(): string;
  cwd(): string;
  // Directory holding the Reasonix Go binary this shell supervises. Portable
  // mode keeps its marker and data folder beside that binary
  // (internal/config/portable.go) and the Go service derives its own home from
  // its executable, so the shell anchors portable resolution there too. An
  // empty value means portable mode cannot resolve a data folder.
  programDir?: string;
  // fileExists probes the portable marker; tests replace it to stay off disk.
  fileExists?(path: string): boolean;
}

const VAR_REF = /\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}/g;

// Mirrors internal/config/portable.go.
const PORTABLE_ENV = "REASONIX_PORTABLE";
const PORTABLE_DIR_ENV = "REASONIX_PORTABLE_DIR";
const PORTABLE_MARKER = "reasonix.portable";
const PORTABLE_DATA_DIR = "reasonix-data";
const PORTABLE_ON = new Set(["1", "true", "on", "yes", "always"]);
const PORTABLE_OFF = new Set(["0", "false", "off", "no", "never"]);

// path.normalize keeps a trailing separator; Go's filepath.Clean does not.
function clean(path: string): string {
  const normalized = normalize(path);
  return normalized.length > 1 && normalized.endsWith(sep) && !/^[A-Za-z]:\\$/.test(normalized) ? normalized.slice(0, -1) : normalized;
}

function expandVars(value: string, env: NodeJS.ProcessEnv): string {
  if (!value.includes("${")) return value;
  return value.replace(VAR_REF, (_match, name: string, fallback: string | undefined) => {
    const found = env[name];
    if (found) return found;
    return fallback ?? "";
  });
}

function userHome(input: HomeEnvironment): string {
  const fromEnv = input.platform === "win32" ? input.env.USERPROFILE : input.env.HOME;
  if (fromEnv && fromEnv.trim() !== "") return fromEnv;
  try {
    return input.homedir();
  } catch {
    return "";
  }
}

function cleanEnvDir(input: HomeEnvironment, name: string): string {
  let dir = (input.env[name] ?? "").trim();
  if (dir === "") return "";
  dir = expandVars(dir, input.env);
  if (dir === "~") {
    const home = userHome(input);
    if (home !== "") dir = home;
  } else if (dir.startsWith("~/") || dir.startsWith("~\\")) {
    const home = userHome(input);
    if (home !== "") dir = home + sep + dir.slice(2);
  }
  if (!isAbsolute(dir)) dir = resolve(input.cwd(), dir);
  return clean(dir);
}

function resolvedProgramDir(input: HomeEnvironment): string {
  const dir = (input.programDir ?? "").trim();
  if (dir === "" || !isAbsolute(dir)) return "";
  return clean(dir);
}

// Mirrors PortableModeEnabled: an explicit on/off flag wins, otherwise the
// marker beside the executable decides. Unset, unknown, and "auto" fall through
// to the marker.
function portableModeEnabled(input: HomeEnvironment): boolean {
  const flag = (input.env[PORTABLE_ENV] ?? "").trim().toLowerCase();
  if (PORTABLE_ON.has(flag)) return true;
  if (PORTABLE_OFF.has(flag)) return false;
  const dir = resolvedProgramDir(input);
  if (dir === "") return false;
  return (input.fileExists ?? existsSync)(join(dir, PORTABLE_MARKER));
}

// Mirrors portableHomeDir and PortableDataDir: the effective portable home, or
// "" when portable mode is off or the folder cannot be resolved.
function portableHome(input: HomeEnvironment): string {
  if (!portableModeEnabled(input)) return "";
  const override = cleanEnvDir(input, PORTABLE_DIR_ENV);
  if (override !== "") return override;
  const dir = resolvedProgramDir(input);
  if (dir === "") return "";
  return clean(join(dir, PORTABLE_DATA_DIR));
}

// Mirrors internal/config.ReasonixHomeDir so the hello `instance.home` matches
// what the Go service computes for the same environment: REASONIX_HOME, then
// the portable data folder, then the platform default.
export function reasonixHome(input: HomeEnvironment): string {
  const explicit = cleanEnvDir(input, "REASONIX_HOME");
  if (explicit !== "") return explicit;
  const portable = portableHome(input);
  if (portable !== "") return portable;
  const home = userHome(input);
  if (input.platform === "win32") {
    const appData = (input.env.APPDATA ?? "").trim();
    if (appData !== "") return clean(appData + sep + "reasonix");
    if (home !== "") return clean(home + sep + "AppData" + sep + "Roaming" + sep + "reasonix");
    return "";
  }
  if (home !== "") return clean(home + sep + ".reasonix");
  const xdg = (input.env.XDG_CONFIG_HOME ?? "").trim();
  if (xdg !== "") return clean(xdg + sep + "reasonix");
  return "";
}
