<#
.SYNOPSIS
    一键构建 Reasonix Desktop（Windows）。

.DESCRIPTION
    Wails 退役后 desktop 改用 Electron shell + Go host-rpc service，本脚本把
    整条链路串起来：

        主机契约 → Go service → 前端(dist) → Electron shell → 打包 → 归位 service

    上游的官方入口 scripts/desktop-build.sh 是 bash，且面向 CI 发布（签名、
    分平台产物、NSIS 双 pass）；本脚本是它在 Windows 上的本地等价物，只关心
    “把这台机器上的桌面端构建出来并可直接运行”。

    依赖：Go >= 1.25、Node >= 24、pnpm 10、git。
    Go 依赖走 GOPROXY，Electron 发行包走 ELECTRON_MIRROR（二者直连均不可达时
    必须设置，见下方 -GoProxy / -ElectronMirror 参数）。

.PARAMETER Version
    版本号，形如 v1.38.6 或 v1.38.6-dev。缺省由最近的 desktop-v* 标签推导；
    若当前提交已越过该标签或工作区有未提交改动，自动追加 -dev。

.PARAMETER Channel
    渠道标识，写入 service 的 -X main.channel 与打包的 build.json。默认 dev。

.PARAMETER Platform
    打包目标，默认 windows/amd64。可选 windows/amd64、windows/arm64；
    Electron 无法跨平台打包，darwin/* 与 linux/* 只能在其原生主机上构建。

.PARAMETER GoProxy
    GOPROXY 值，默认 https://goproxy.cn,direct。

.PARAMETER ElectronMirror
    ELECTRON_MIRROR 值，默认 npmmirror 镜像。

.PARAMETER SkipInstall
    跳过 pnpm install（node_modules 已是最新时可省一次校验）。

.PARAMETER NoPackage
    只构建（service + 前端 + shell），不执行 @electron/packager 打包。

.PARAMETER NoBuild
    只打包。package.mjs 内部会自行构建前端与 shell，跳过脚本里显式的那一遍。

.PARAMETER Run
    构建完成后启动打包产物，使用独立的临时 REASONIX_HOME，不触碰真实数据。

.EXAMPLE
    .\scripts\build-desktop.ps1
    全量构建并打包，版本与渠道自动推导。

.EXAMPLE
    .\scripts\build-desktop.ps1 -Version v1.38.6 -Channel stable -Run
    以 release 版本与 stable 渠道构建，完成后立即启动。

.EXAMPLE
    .\scripts\build-desktop.ps1 -NoPackage -SkipInstall
    快速迭代 Go/前端改动，不打包。
#>
[CmdletBinding()]
param(
    [string]$Version = "",
    [string]$Channel = "dev",
    [ValidateSet("windows/amd64", "windows/arm64")]
    [string]$Platform = "windows/amd64",
    [string]$GoProxy = "https://goproxy.cn,direct",
    [string]$ElectronMirror = "https://registry.npmmirror.com/-/binary/electron/",
    [switch]$SkipInstall,
    [switch]$NoPackage,
    [switch]$NoBuild,
    [switch]$Run
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

# Native commands do not throw on a non-zero exit the way cmdlets do, so every
# external call goes through this wrapper and stops the build on failure.
function Invoke-External {
    param(
        [Parameter(Mandatory)][string]$Label,
        [Parameter(Mandatory)][string]$Command,
        [string[]]$Arguments = @()
    )
    Write-Host "==> $Label" -ForegroundColor Cyan
    & $Command @Arguments
    $code = $LASTEXITCODE
    if ($code -ne 0) {
        throw "$Label failed with exit code $code"
    }
}

function Write-Section {
    param([Parameter(Mandatory)][string]$Text)
    Write-Host ""
    Write-Host "-- $Text" -ForegroundColor White
}

$repoRoot = Split-Path -Parent $PSScriptRoot
$desktopDir = Join-Path $repoRoot "desktop"
$serviceExe = Join-Path $desktopDir "build/bin/reasonix-desktop-service.exe"

Write-Section "Reasonix desktop build"
Write-Host "repo     $repoRoot"
Write-Host "platform $Platform"
Write-Host "channel  $Channel"

# --- 前置检查 ---------------------------------------------------------------
foreach ($tool in @("git", "go", "node", "pnpm")) {
    if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) {
        throw "$tool was not found on PATH; install it before building the desktop app"
    }
}

$nodeMajor = [int]((& node --version).TrimStart("v").Split(".")[0])
if ($nodeMajor -lt 24) {
    Write-Warning "Node $(& node --version) is below the required >= 24; the build may still work but is unsupported"
}
# pnpm switches to the version in the nearest packageManager field, so this has
# to be read from desktop/ rather than the repo root: the root has no
# package.json and would report the globally installed pnpm instead.
Push-Location $desktopDir
try { $pnpmVersion = (& pnpm --version).Trim() } finally { Pop-Location }
$pnpmMajor = [int]($pnpmVersion.Split(".")[0])
if ($pnpmMajor -ne 10) {
    Write-Warning "pnpm $pnpmVersion differs from the pinned major 10; desktop/package.json declares packageManager pnpm@10"
}

# --- 版本推导 ---------------------------------------------------------------
if (-not $Version) {
    $described = ""
    try { $described = (& git -C $repoRoot describe --tags --match "desktop-v*" 2>$null) } catch { $described = "" }
    if (-not $described) {
        try { $described = (& git -C $repoRoot describe --tags --match "v*" 2>$null) } catch { $described = "" }
    }
    if ($described) {
        # desktop-v1.38.6-6-gabcdef -> v1.38.6, with -6-gabcdef marking commits
        # past the tag. A release tag may itself carry a prerelease suffix that
        # must survive, so only the trailing -N-gHASH is stripped.
        $stripped = $described -replace "^desktop-", ""
        $pastTag = $stripped -match "-\d+-g[0-9a-f]+$"
        $Version = $stripped -replace "-\d+-g[0-9a-f]+$", ""
    } else {
        $Version = "v0.0.0"
        $pastTag = $true
    }
    if ($Version -notmatch "^v\d+\.\d+\.\d+") {
        throw "could not derive a vX.Y.Z version from git (got '$described'); pass -Version explicitly"
    }
    $tracked = (& git -C $repoRoot status --porcelain --untracked-files=no)
    if ($pastTag -or $tracked) {
        if ($Version -notmatch "-") { $Version = "$Version-dev" }
    }
}
if ($Version -notmatch "^v\d+\.\d+\.\d+") {
    throw "-Version must look like vX.Y.Z or vX.Y.Z-suffix, got '$Version'"
}
Write-Host "version  $Version"

# --- 环境 -------------------------------------------------------------------
# Both proxies are set through the process environment only; a machine-level
# GOPROXY/ELECTRON_MIRROR already present is kept unless the parameters are
# overridden on the command line.
if (-not $env:GOPROXY) { $env:GOPROXY = $GoProxy }
if (-not $env:ELECTRON_MIRROR) { $env:ELECTRON_MIRROR = $ElectronMirror }
if (-not $env:NODE_OPTIONS) { $env:NODE_OPTIONS = "--max-old-space-size=4096" }
$env:CGO_ENABLED = "0"
Write-Host "GOPROXY  $env:GOPROXY"
Write-Host "ELECTRON_MIRROR $env:ELECTRON_MIRROR"

# --- 1. 依赖 -----------------------------------------------------------------
if (-not $SkipInstall) {
    Write-Section "Install workspace dependencies"
    Push-Location $desktopDir
    try {
        Invoke-External -Label "pnpm install --frozen-lockfile" -Command "pnpm" -Arguments @("install", "--frozen-lockfile")
    } finally { Pop-Location }
} else {
    Write-Section "Install workspace dependencies (skipped)"
}

# --- 2. 主机契约 -------------------------------------------------------------
# The host-RPC registry requires ownership metadata for every App method, and
# the packaged shell embeds a digest of the contract. A contract that lags the
# Go source fails the desktop test suite and hangs the host-RPC handshake, so
# regenerate it here and surface drift instead of letting packaging trip on it.
Write-Section "Regenerate host contract"
Push-Location $desktopDir
try {
    Invoke-External -Label "go run . -emit-contract frontend/src/generated" -Command "go" -Arguments @("run", ".", "-emit-contract", "frontend/src/generated")
} finally { Pop-Location }

$contractDrift = & git -C $repoRoot status --porcelain -- desktop/frontend/src/generated desktop/host_command_owners.generated.json
if ($contractDrift) {
    Write-Warning "the host contract changed; commit these before releasing:"
    $contractDrift | ForEach-Object { Write-Warning "  $_" }
}

# --- 3. Go service -----------------------------------------------------------
# The .exe suffix is required on Windows: the Electron shell's default lookup
# and electron/scripts/start.mjs both look for reasonix-desktop-service.exe.
if (-not $NoBuild) {
    Write-Section "Build Go desktop service"
    Push-Location $desktopDir
    try {
        New-Item -ItemType Directory -Force -Path (Split-Path -Parent $serviceExe) | Out-Null
        Invoke-External -Label "go build -o build/bin/reasonix-desktop-service.exe ." -Command "go" `
            -Arguments @("build", "-o", "build/bin/reasonix-desktop-service.exe", ".")
    } finally { Pop-Location }
    Write-Host "    $serviceExe"

    # --- 4. 前端 + Electron shell -------------------------------------------
    # The aggregate build runs build:electron (frontend, gated by the bundle
    # budget) and then the shell build (esbuild bundles main/preload and embeds
    # desktopContract.json from the contract regenerated above).
    Write-Section "Build frontend and Electron shell"
    Push-Location $desktopDir
    try {
        Invoke-External -Label "pnpm build" -Command "pnpm" -Arguments @("build")
    } finally { Pop-Location }
} else {
    Write-Section "Build Go service, frontend and Electron shell (skipped; packaging rebuilds the UI)"
}

# --- 5. 打包 -----------------------------------------------------------------
$appDir = Join-Path $desktopDir "build/electron/$($Platform -replace '/', '-')/app"
if (-not $NoPackage) {
    Write-Section "Package Electron app"
    # package.mjs rebuilds the frontend and shell itself and then runs
    # @electron/packager, which downloads the Electron distribution through
    # ELECTRON_MIRROR on first use.
    Invoke-External -Label "package.mjs $Platform $Version $Channel" -Command "node" `
        -Arguments @("desktop/packaging/package.mjs", $Platform, $Version, $Channel)
    Write-Host "    $appDir"

    # desktop-build.sh performs this lookup step for the release bundles; the
    # packaged shell resolves its service from resources/service/ when it is
    # not handed one through REASONIX_DESKTOP_SERVICE.
    Write-Section "Stage the service into the packaged app"
    $stagedService = Join-Path $appDir "resources/service/reasonix-desktop.exe"
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $stagedService) | Out-Null
    Copy-Item -LiteralPath $serviceExe -Destination $stagedService -Force
    Write-Host "    $stagedService"
} else {
    Write-Section "Package Electron app (skipped)"
}

# --- 6. 结果 -----------------------------------------------------------------
$shellExe = Join-Path $appDir "Reasonix.exe"
Write-Section "Done"
Write-Host "service $serviceExe"
if (-not $NoPackage) {
    Write-Host "app     $shellExe"
    Write-Host ""
    Write-Host "Run it with an isolated data home:"
    Write-Host "  `$env:REASONIX_HOME='$env:TEMP\reasonix-desktop-dev'; & '$shellExe'"
    Write-Host "Or run against the built sources (no packaging):"
    Write-Host "  cd $desktopDir/electron; pnpm start"
}

if ($Run -and -not $NoPackage) {
    if (-not (Test-Path -LiteralPath $shellExe)) { throw "packaged shell not found at $shellExe" }
    Write-Section "Launch"
    # A dedicated REASONIX_HOME keeps this build away from real config,
    # credentials and sessions, the same way CONTRIBUTING.md isolates dev runs.
    $runHome = Join-Path $env:TEMP "reasonix-desktop-dev"
    $env:REASONIX_HOME = $runHome
    Write-Host "REASONIX_HOME=$runHome"
    & $shellExe
}
