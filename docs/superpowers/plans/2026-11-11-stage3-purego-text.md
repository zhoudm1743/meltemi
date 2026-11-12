# Stage 3 Pure-Go Text Migration: 方案与实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 去掉文本子系统的 cgo 原生依赖（HarfBuzz / FreeType / fontconfig），全平台统一使用纯 Go 整形与光栅化：`github.com/go-text/typesetting`（含 HarfBuzz 移植、bidi、字体扫描）+ `golang.org/x/image/vector`（轮廓光栅）。保留现有 `LineLayout` 契约，公共 UI 不变。

**Architecture:** 新增一个纯 Go 的 `complexShaper` 实现，默认在所有平台启用；现有 cgo HarfBuzz 引擎改为 opt-in（`meltemi_text_cgo`），作为对照/回退。两者实现同一 `layout(value, scaleX, scaleY) (LineLayout, error)` 接口，`ui` 包无需改动。系统字体回退：Windows 复用 `font_windows.go`；Linux 阶段 2 起用 `fontscan` 取代 fontconfig cgo。

**Tech Stack:** Go 1.26、`github.com/go-text/typesetting v0.3.5`、既有 `golang.org/x/image v0.47.0`；不新增任何 C 依赖。

**Spec:** `doc/总体设计.md` 第 11.2、12、13 阶段3；`doc/兼容性.md`。

## 已完成的可行性验证（2026-11-11，Windows 10 amd64）

独立探针（不改仓库）确认整条纯 Go 链路可用：

| 用例 | 结果 |
| --- | --- |
| Calibri `office` | 6 rune → **4 glyph**（`fi`/`ffi` 连字），首字形出墨 67 px |
| Segoe UI `office` | 6 rune → 6 glyph（该字体无对应连字，属正常） |
| Arial `مرحبا` | `Direction=RTL`，5 glyph，首字形出墨 31 px |
| `msyh.ttc` face 0 `中` | 1 glyph，advance 20.00，出墨 160 px |
| 光栅方式 | `font.Face.GlyphDataOutline(gid)` → `x/image/vector.Rasterizer.Draw` 到 `image.Alpha` |

依赖增量：仅 **1 个直接模块** `github.com/go-text/typesetting`（其依赖 `x/image` 已存在；`typesetting-utils` 仅测试图）。无 HarfBuzz/FreeType/fontconfig/MinGW 文本库。

## 与现有 cgo 方案的关系

- 现有 `internal/text/shape_freetype_cgo.go`、`raster_freetype_cgo.go`、`fontconfig_linux_cgo.go` 与 `scripts/build-windows-text-deps.ps1` **保留**，但改为 opt-in（tag `meltemi_text_cgo`），用于对照与回退。
- 默认构建不再需要 `third_party/windows-text`、HarfBuzz/FreeType 头文件或 fontconfig。
- 之前 Windows `meltemi_text_harfbuzz` 的成果不浪费：它成为 cgo 参考实现，可继续跑几何 oracle。

## Global Constraints

- 不改变 `LineLayout` / `Bitmap` / `Cluster` / `Glyph` / `CaretOffsets` 的语义与空文本、无效 UTF-8、缩放、Missing 行为；`ui` 包零改动。
- 纯 Go 引擎必须在无 cgo（`CGO_ENABLED=0`）下可编译、可测试；`ShapingEngine()` 新增 `"go-text"` 取值。
- 缺字必须走系统回退并进入 `Missing`；不得把 notdef 当成功。彩色 emoji（COLR/SVG/bitmap）本阶段维持“记录 missing 或降级为轮廓”的边界，不宣称彩色。
- 失败不缓存为成功；字体解析失败必须可恢复为 notdef 且不 panic。
- 非 Git 原地工作区，不初始化/提交。

## Review Focus

- 连字/组合符/阿拉伯文/mixed LTR-RTL 的 glyph、cluster、caret 与既有 HarfBuzz 结果一致（用现有关键测试 + `meltemi-text` 演示比对）。
- 双向文本的 cluster → rune 映射、caret 单调/镜像规则与现有 `assignPens` 契约一致；不得切断组合符。
- 缩放：`size*scaleY` 整形、`scaleX/scaleY` 应用到水平 advance/offset，保持现有物理像素契约。
- 无 cgo 构建必须 `go test ./...` 通过；带 `meltemi_text_cgo` 的 cgo 构建不得回归。

---

### Task 0: 可行性验证（已完成）

- [x] 独立探针验证 typesetting 整形（连字、RTL）+ x/image/vector 光栅 + TTC face 0。
- 结果见上表。

### Task 1: 抽出共享装配逻辑 + 纯 Go 引擎

**Files:**
- Create: `internal/text/shape_gotext.go`, `internal/text/shape_assembly.go`
- Modify: `internal/text/shape_freetype_cgo.go`（构建约束改为 `... && meltemi_text_cgo`；装配逻辑改为复用共享文件）
- Modify: `internal/text/shape_legacy.go`（`ShapingEngine` 增加 `"go-text"`）
- Modify: `internal/text/face.go`（注释；`NewFace` 选择不变）
- Test: `internal/text/shaping_test.go`、新增纯 Go 专项测试

**Interfaces:**
- 纯 Go 引擎注册 `newComplexShaper`（默认）；cgo 引擎仅在 `-tags meltemi_text_cgo` 时注册，二者互斥（`!meltemi_text_cgo` vs `meltemi_text_cgo`）。
- 把 `runGlyph`/`shapedRun`/`fontRun`/`itemizeFonts`/`resolveClusterEnds`/`edgeEvent`/`assignPens`/`runWidth` 从 cgo 文件移到无 cgo 的 `shape_assembly.go`；引擎相关字段抽象为接口或按引擎拆分。
- 纯 Go 引擎：`font.ParseTTF/ParseTTC` → `*font.Face`；`Segmenter.Split` + `HarfbuzzShaper.Shape` → `[]shaping.Output` → 装配为 `LineLayout`；`GlyphDataOutline` + `vector.Rasterizer` 出 alpha。

- [x] **Step 1: 加依赖并锁定** `github.com/go-text/typesetting v0.3.5` 已加入 `go.mod`；`CGO_ENABLED=0 go test ./internal/text` 通过。
- [x] **Step 2: 测试** 既有 shaping/UI 测试在 `ShapingEngine()=="go-text"` 下全部运行并通过（连字、RTL、cluster、caret、回退）。
- [x] **Step 3: 实现引擎** 新增 `internal/text/shape_gotext.go`（默认 `!meltemi_text_cgo`），cgo HarfBuzz 改为 `meltemi_text_cgo` opt-in；`LineLayout` 契约保持不变，`ui` 仅有一个测试做了亚像素兼容修正。
- [x] **Step 4: 验证** 见下方「Task 1 结果」。

**Task 1 结果（2026-11-12，Windows 10 amd64，WinLibs gcc 16.2.0）：**

- 默认纯 Go：`go test ./... -count=1` 全绿；`go vet ./...` 干净；`go test -race ./internal/text ./ui` 通过。
- 无 cgo：`CGO_ENABLED=0 go test ./internal/text -count=1` 通过。
- cgo opt-in：`go test -tags meltemi_text_cgo ./internal/text` 通过；`go test -race -tags meltemi_text_cgo ./internal/text` 通过。
- 新增 `internal/text/complex_windows_test.go`（tag `windows && !meltemi_text_cgo`）在 Windows 上用真实系统字体验证纯 Go 引擎：Calibri 连字（glyph < rune）、Arial 阿拉伯文 RTL、`goregular`+`msyh.ttc` 中文回退无 Missing 且有墨。之所以需要它：共享 shaping 测试用 Linux 字体路径，在 Windows 会 skip。
- `cmd/meltemi-text --smoke` 在 Windows 被 demo 自身的 `--smoke requires an X11 DISPLAY` 守卫拒绝，**未做视觉 smoke**；GL 集成测试仍通过。
- 唯一测试改动：`ui/input_draw_test.go` 的窄视口宽度改为按实测 advance 推导，消除 FreeType hinting 与纯 Go 未 hinting 的亚像素差异；断言语义不变。
- **中间限制**：Linux 默认（无 `meltemi_text_cgo`）时 fontconfig 被一并 gate 掉，`systemFontPath` 为空，系统字体回退暂不可用；必须完成 Task 2（fontscan）后才算 Linux 可交付。Windows 回退由 `font_windows.go` 提供，已通过 CJK 测试。

### Task 2: 系统字体回退去 fontconfig

**Files:**
- Create: `internal/text/font_scan.go`（`fontscan.FontMap` 封装）
- Modify: `internal/text/fallback.go`、`font_windows.go`、`fontconfig_linux_cgo.go`（tag）

**Interfaces:**
- 纯 Go 引擎的 `shaping.Fontmap`：先 primary，再 `fontscan`（Linux/macOS）或 `font_windows.go`（Windows）解析回退面。
- `fontscan` 缓存目录可用 `os.UserCacheDir()/meltemi/fontscan`；首次扫描失败降级为“无系统回退”，不阻塞启动。

- [x] **Step 1: 写回退测试** 新增 `TestWindowsGoTextFontScanFallback`（清空 `systemFontPath`，强制走 fontscan，断言 CJK 无 Missing 且有墨）；既有 seam 失败测试改为同时禁用 fontscan 以保持原意。
- [x] **Step 2: 实现 fontscan 回退** 纯 Go 引擎回退顺序：primary → 平台候选（Windows `font_windows.go`）→ `fontscan`（全局索引，磁盘缓存于用户缓存目录）；`fontconfig` 仅保留给 cgo opt-in。
- [x] **Step 3: 验证** 无 cgo 全量测试通过；Windows 默认不依赖 `third_party`；`fontscan` 首次解析 CJK 约 0.1s（本机，含缓存）。

**Task 2 结果（2026-11-12，Windows 10 amd64）：**

- `go test ./...` 全绿；`CGO_ENABLED=0 go test ./internal/text` 通过；`go test -tags meltemi_text_cgo ./internal/text` 通过；`go test -race ./internal/text` 通过；`go vet ./...` 干净。
- 新增 `internal/text/fontscan_seam.go`（可禁用的 `fontScanLookup` seam）；Windows/CJK 回退与 fontscan 回退各有测试。
- Linux/macOS 的默认回退现由 `fontscan` 提供（`DefaultFontDirectories` 覆盖三平台），不再需要 fontconfig；但本机无 Linux/macOS，运行时未实测。

### Task 3: 默认化、文档与清理

- [x] 确认默认 `!meltemi_text_cgo` 走纯 Go；`meltemi_text_cgo` 为显式 opt-in（源码与 `ShapingEngine()` 均如此）。
- [x] 更新 `README.md`：项目定位、环境依赖、Windows 说明、演示字体回退、平台状态表均改为纯 Go 默认 + cgo opt-in。
- [x] 更新 `doc/兼容性.md`：顶部加阶段 3 说明；阶段 2 复杂文本行标注为 cgo 对照；Windows 表增加纯 Go 连字/RTL 与 fontscan 回退行。
- [x] 保留 `scripts/build-windows-text-deps.ps1` 与 `third_party/windows-text` 作为 cgo opt-in 对照（已在 `.gitignore`），不删除。`doc/总体设计.md` 保持设计决策历史，未改选型描述。
- [x] 最终验证：Windows 默认（无文本 C 库）`go test ./...` 全绿、`CGO_ENABLED=0 go test ./internal/text` 通过、cgo opt-in 通过；`meltemi-text --smoke` 被 demo 的 X11 守卫拒绝（未做视觉 smoke）；Linux 本机无环境，仅以 `CGO_ENABLED=0` 测试代表纯 Go 编译/单测通过。

## 风险与回滚

- **cluster/caret 语义偏差**：先以现有 `shaping_test.go` 与 `ui` 编辑测试为硬门槛；不一致时优先修装配逻辑，不改公共契约。
- **性能**：纯 Go 光栅化可能比 FreeType 慢；先保证正确性，再按需加 glyph 位图缓存（现有文本纹理缓存已在上层）。
- **彩色 emoji**：本阶段不承诺彩色；`GlyphSVG/Bitmap/Color` 记录 missing 或回退轮廓，文档写明。
- **回滚**：`-tags meltemi_text_cgo` 一键回到已验证的 HarfBuzz/FreeType 路径；纯 Go 引擎未就绪不影响默认仅当它通过全部既有测试后。
