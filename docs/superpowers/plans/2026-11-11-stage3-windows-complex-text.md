# Stage 3 Windows Complex Text: 方案与实施计划

> **状态（2026-11-12）**：本计划描述的 cgo HarfBuzz/FreeType 路径已从默认改为 opt-in，构建标签统一为 `meltemi_text_cgo`。默认文本引擎见 `2026-11-11-stage3-purego-text.md`（纯 Go）。本文保留作为对照/回退实现与几何 oracle 的依据。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 Windows(cgo) 上的 `internal/text` 具备与 Linux 一致的复杂文本能力：HarfBuzz 整形（连字、组合符、阿拉伯文形态、双向）、FreeType 按 glyph ID 光栅化、系统字体逐字符回退；不再退回 legacy 引擎、不再跳过 RTL 几何测试。

**Architecture:** 复用 Linux+cgo 的同一份引擎实现，把平台无关的整形/光栅文件从 `linux && cgo` 扩到 `(linux || windows) && cgo`；字体来源仍走既有 `systemFontPath` seam（Linux=fontconfig，Windows=现有 `font_windows.go`），不改 public API，不新增 Go 依赖。Windows 依赖通过 opt-in 构建标签接入，默认 Windows 构建仍只需 MinGW。

**Tech Stack:** Go 1.26、HarfBuzz、FreeType、WinLibs MinGW-w64 (UCRT/POSIX)、既有 x/image 测试 fake；不新增 Go 依赖。

**Spec:** `doc/总体设计.md` 第 3.1、5.5、11.2、12、13 阶段3；`doc/兼容性.md` 「Windows 10 amd64」。

## 方案对比

| 方案 | 做法 | 优点 | 缺点 | 结论 |
| --- | --- | --- | --- | --- |
| **A. 复用 HarfBuzz+FreeType（推荐）** | 把 `shape_linux_cgo.go`/`raster_linux_cgo.go` 的构建约束扩到 Windows，新增 Windows 版字体回退（复用 `windowsSystemFont`），用 opt-in tag 接入 | 与 Linux 行为/测试完全一致；现有 `complexShaper` 接口和几何 oracle 可复用；不新增 Go 依赖 | 需要在 Windows 上获取 HarfBuzz/FreeType 头文件与库；需处理 TTC face index | **先做，opt-in 落地后再默认化** |
| B. DirectWrite | 用 Win32 COM（IDWriteTextLayout）做整形与排版 | 无第三方 C 依赖，Windows 原生 | 需要大量 COM/cgo 绑定；与 Linux 引擎行为分叉；RTL/cluster 逻辑要重写；无头测试难 | 不选 |
| C. 纯 Go 整形（如 go-text/typesetting） | 用纯 Go 移植的 HarfBuzz 替代 C 引擎 | 无 C 依赖、跨 macOS 更省事 | 要替换 Linux 现网 HarfBuzz 路径与光栅；偏离既定"依赖 HarfBuzz+FreeType"决策；大改 | 长期备选，不在本阶段 |

推荐 A：它是唯一能在不重写引擎、不改变 Linux 已验证行为的前提下补齐 Windows 的路径。

## Global Constraints

- 不在公共 API 暴露 HarfBuzz/FreeType/C 类型；所有 C 资源有明确 UI 线程所有权与 `Close`，失败回滚。
- **保持默认 Windows 构建只需 MinGW**：复杂文本用 opt-in tag `meltemi_text_harfbuzz`（`//go:build (linux && cgo) || (windows && cgo && meltemi_text_harfbuzz)`）；未开 tag 时 Windows 仍走 legacy，现有承诺不回退。
- 缺字必须走系统回退，不得把 notdef 当成功；彩色 emoji（`FT_PIXEL_MODE_BGRA`）维持"记录 missing + 灰度/notdef"边界，本阶段不做彩色合成。
- 静态链接 HarfBuzz(MIT) / FreeType(FTL) 时须附带各自许可证文本；不提交不确定来源的二进制。
- 非 Git 原地工作区，不初始化/提交。

## Review Focus

- 同字符串在不同字体/方向下的 glyph 与 cluster 顺序，Windows 结果须与 Linux 一致；由扩展后的 `TestDrawingGeometryInvariants`/`TestMarkOffsetConvention` 固定。
- TTC 回退：`windowsSystemFont` 命中的面必须与实际打开的 face index 一致，否则出现"报覆盖却画 tofu"；由新增 TTC 测试固定。
- 未开 tag 的 Windows 构建必须仍能 `go test ./...` 通过；tag 开/关两条路径都要绿。

---

### Task 0: 依赖获取验证（先探路，再定分发策略）

**Files:**
- Create: `scripts/build-windows-text-deps.ps1`（或 `.md` 安装说明，视验证结果）
- Modify: 无

**Interfaces:**
- 产出可被 cgo 使用的 HarfBuzz+FreeType 头文件目录与静态库；具体路径通过 `CGO_CFLAGS`/`CGO_LDFLAGS` 或 `${SRCDIR}/third_party` 传入。

- [x] **Step 1: 评估三条获取路径并实测其中一条**：① MSYS2 `mingw-w64-ucrt-x86_64-harfbuzz`/`-freetype`（确认是否提供可被 WinLibs gcc 链接的 `.a`）；② 用现有 WinLibs gcc + meson/cmake 从源码构建静态库；③ vcpkg `x64-mingw-static`。记录实际可用的命令与产物路径。
- [x] **Step 2: 写最小 cgo 探针**（临时 main，不进仓库或进 `scripts/`）：`#include <hb.h>` + `#include <ft2build.h>`，链接并调用 `hb_version_string()`、`FT_Init_FreeType`，确认 WinLibs gcc 能静态链接运行。
- [x] **Step 3: 记录选型**：把选定路径、精确包/版本、产物路径、许可证文本位置写入本计划"依赖记录"小节，并决定 `third_party/` 是否入库（倾向 gitignore + 脚本重建）。

**依赖记录（2026-11-11 实测）：**

- 选定路径：**用现有 WinLibs 工具链从源码构建静态库**（方案 ②），不引入 MSYS2/vcpkg/Python。
  实测机：WinLibs MinGW-W64 x86_64-ucrt-posix-seh gcc 16.2.0；其 `bin` 自带
  `cmake 4.4.4`、`ninja`、`mingw32-make`、`ar`、`ranlib`、`pkgconf 3.0.8`。
- 版本：HarfBuzz **12.3.2**、FreeType **2.13.3**（与 Linux 实测版本一致）。
- 复现脚本：`scripts/build-windows-text-deps.ps1`（下载 → `cmake -E tar` 解压 →
  cmake+ninja 静态构建 → 安装到 `third_party/windows-text/`，已 gitignore）。
- 产物布局：
  - `third_party/windows-text/include/harfbuzz/hb.h`、`hb-ft.h`
  - `third_party/windows-text/include/freetype2/ft2build.h`
  - `third_party/windows-text/lib/libharfbuzz.a`、`libfreetype.a`
- cgo 探针（已实际编译运行，exit 0）：
  `CGO_CFLAGS=-I.../include/harfbuzz -I.../include/freetype2`
  `CGO_LDFLAGS=-L.../lib -lharfbuzz -lfreetype -lstdc++ -lm`
  输出 `hb_version=12.3.2`、`ft_version=2.13.3`。HarfBuzz 为 C++ 实现，静态链接必须带
  `-lstdc++`（否则 `__gxx_personality` 等未定义）。
- 未采用：MSYS2（需第二个工具链，WinLibs 已够用）、vcpkg（MinGW triplet 支持弱且需额外 bootstrap）。
- 许可证：HarfBuzz=MIT，FreeType=FTL；分发静态库时须附带许可证文本。

### Task 1: 引擎跨平台化与 Windows 回退

**Files:**
- Modify: `internal/text/shape_linux_cgo.go` → `internal/text/shape_freetype_cgo.go`（改构建约束，`#cgo` 按 OS 分流）
- Modify: `internal/text/raster_linux_cgo.go` → `internal/text/raster_freetype_cgo.go`
- Modify: `internal/text/font_windows.go`（回退返回 face index）
- Modify: `internal/text/fallback.go`（`systemFontPath` → 携带 face index 的 `FontRef`）
- Modify: `internal/text/fontconfig_linux_cgo.go`（适配 seam，face index 取 0 / `FC_INDEX`）
- Modify: `internal/text/face.go`（注释与 NewFace 说明，行为不变）
- Test: `internal/text/shape_*_test.go`、`internal/text/font_windows_test.go`

**Interfaces:**
- `systemFontPath` 由 `func(rune) (string, error)` 改为 `func(rune) (FontRef, error)`，`FontRef{Path string; FaceIndex int}`；`fallbackResolver` 缓存 `FontRef`；`newHBEngine` 接收 face index 并传给 `FT_New_Memory_Face`。
- 构建约束统一为 `(linux && cgo) || (windows && cgo && meltemi_text_harfbuzz)`。
- Windows 字体回退仍使用 `font_windows.go` 的候选列表；Linux 仍用 fontconfig。

- [x] **Step 1: 写失败测试**：新增 Windows TAG 下断言 `ShapingEngine()=="harfbuzz"`、CJK 系统回退（TTC face index）、阿拉伯文 `Direction==RTL`；见 `internal/text/complex_windows_cgo_test.go`。
- [x] **Step 2: 运行带 tag 的 focused 测试**：`go test -tags meltemi_text_harfbuzz ./internal/text -run TestWindowsHarfbuzz -v` 通过（注意：测试与实现一并落地，未单独保留 RED 快照）。
- [x] **Step 3: 实现平台化**：`shape_linux_cgo.go`→`shape_freetype_cgo.go`、`raster_linux_cgo.go`→`raster_freetype_cgo.go`；构建约束 `(linux && cgo) || (windows && cgo && meltemi_text_harfbuzz)`；`#cgo windows` 指向 `${SRCDIR}/../../third_party/windows-text` 并带 `-lstdc++`；`FontRef{Path,FaceIndex}` 贯穿回退与 `newHBEngine`；fontconfig 返回 index 0。
- [x] **Step 4: 两端验证**：见下方「Task 1 结果」。

**Task 1 结果（2026-11-11，Windows 10 amd64，WinLibs gcc 16.2.0）：**

- 无 tag：`go test ./... -count=1` 全绿；`go vet ./...` 干净；默认 Windows 仍走 legacy。
- 带 tag：`go test -tags meltemi_text_harfbuzz ./... -count=1` 全绿；`go build -tags meltemi_text_harfbuzz ./...` 成功；`go vet -tags meltemi_text_harfbuzz ./internal/text` 干净。
- 关键通过项：`TestWindowsHarfbuzzEngineActive`、`TestWindowsHarfbuzzCJKFallback`（`msyh.ttc` 按 face index 打开，`Missing` 为空且有墨）、`TestWindowsHarfbuzzArabicDirection`（RTL 且有墨）。
- Linux 侧仅做了构建约束与源码复核（本机无 Linux）；由既有 `linux && cgo` 路径保持不变。
- 已知基线：`gofmt -l internal/text` 仅报 `face_test.go`（CRLF/未触碰的既有问题），本次改动文件均已 gofmt。

### Task 2: 测试矩阵与文档

**Files:**
- Modify: `internal/text/geometry_oracle_linux_cgo_test.go` → `geometry_oracle_freetype_cgo_test.go`（约束扩到 Windows）
- Modify: `internal/text/shaping_test.go`（字体候选按 OS 选择；Windows 用 `windowsSystemFont` 或 `C:\Windows\Fonts` 候选）
- Modify: `doc/兼容性.md`（Windows 表格：整形/RTL/连字从"未实现"改为证据等级）
- Modify: `README.md`（Windows 依赖与 opt-in tag 说明）

- [ ] **Step 1: 扩测试约束**：让几何 oracle（`TestDrawingGeometryInvariants`、`TestMarkOffsetConvention`）在 Windows+cgo+tag 下运行，不再跳过 RTL。
- [ ] **Step 2: 字体候选平台化**：测试内按 `runtime.GOOS` 选 Linux/Windows 实际存在的字体，缺失时 skip 并记录，不硬编码单机路径。
- [ ] **Step 3: 跑真实证据**：Windows 带 tag `go test ./... -count=1` + `MELTEMI_GLFW_DESKTOP_TESTS=1 go run ./cmd/meltemi-text --smoke`；只有实际跑过才更新 `doc/兼容性.md`，并区分自动化/协议/视觉。
- [ ] **Step 4: 文档收口**：README 写清"默认只需 MinGW；复杂文本需安装 HarfBuzz/FreeType 并加 tag"，兼容矩阵只写实测结论。

### Task 3: 默认化与 macOS 复用评估（后续）

- [ ] 依赖打包稳定后，把 `meltemi_text_harfbuzz` 从 opt-in 改为 Windows 默认，或提供 vendor 静态库 + 脚本，降低使用者门槛。
- [ ] 评估同一 `complexShaper` 能否用于 macOS（FreeType/HarfBuzz 可静态链接），避免为 macOS 再写一套整形。

## 风险与回滚

- 依赖来源不稳：Windows 复杂文本始终在 opt-in tag 后，默认构建不受影响，可随时回滚到 legacy。
- TTC/face index：若 `FontRef` 改造过大，可先用"仅当 face 0 覆盖才返回路径"的保守策略作为临时方案，但必须记录 coverage 与实开面一致。
- 未验证声明：Windows 视觉 smoke 与彩色 emoji 仍属边界，不因单元测试通过就标"视觉通过"。
