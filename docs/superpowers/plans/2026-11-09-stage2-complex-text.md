# Stage 2 Complex Text and Font Fallback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用真实字形整形支持连字、组合字符、阿拉伯文形态和双向文本，并提供系统字体发现与逐字符回退，不再要求应用为中文手工注册字体。

**Architecture:** Linux/cgo 下 `internal/text` 使用 HarfBuzz 进行 cluster/direction/advance 整形，FreeType 按 glyph ID 光栅化，fontconfig 查找回退字体。Face 对内管理 primary/fallback 字体和缓存；公共 UI 仍只使用 Face，不暴露 C 类型。非 cgo/非 Linux 保留现有路径，能力边界明确。

**Tech Stack:** Go 1.26、系统 HarfBuzz 12.3.2、FreeType、fontconfig、既有 x/image 测试 fake；不新增 Go 依赖。

**Spec:** `doc/总体设计.md` 第 3.1、5.4、5.5、11.2、12、13 阶段2、15节。

## Global Constraints

- 不新增 Go 依赖，不在公共 API 暴露 HarfBuzz/FreeType/fontconfig/C 类型。
- 所有 C 资源有明确 UI 线程所有权和 Close；失败必须回滚，不使用失败后 C 指针。
- 缺字使用系统回退；不得把 tofu 当成功。复杂 cluster 的删除/选择不得切断 grapheme。
- 保持现有 CSS font-family、RegisterFont、Password masking、IME preedit 和逻辑尺寸行为。
- 非 Git 原地工作区，不初始化/提交。

## Review Focus

- `ffi` 连字、中文、emoji、阿拉伯文和 mixed LTR/RTL：glyph/cluster/绘制顺序一致；Task 1 的 `TestHarfBuzzShapesRealScripts` 固定。
- 系统字体缺失/无效字体文件/缓存关闭：不得 panic、泄漏或返回半初始化 Face；Task 1 的 `TestFontFallbackDiscoveryAndLifecycle` 固定。
- 组合字符与双向文本编辑：光标/选择/Backspace 不切断 cluster；Task 2 的 `TestComplexClusterEditingAndHitTesting` 固定。

---

### Task 1: HarfBuzz/FreeType 引擎与 fontconfig 回退

**Files:**
- Create: `internal/text/shape_linux_cgo.go`, `internal/text/raster_linux_cgo.go`, `internal/text/fontconfig_linux_cgo.go`, `internal/text/fallback.go`, `internal/text/shape_legacy.go`
- Modify: `internal/text/face.go`, `internal/text/face_test.go`, `ui/app.go`

**Interfaces:**
- 内部 `Face.Layout(value string) (LineLayout,error)`；LineLayout 包含逻辑 Bitmap、glyph cluster 起止 rune、方向、caret offsets 和 hit-test 所需的 cluster 边界。
- Face 保留 `Rasterize/CaretOffsets` 兼容接口并委托 Layout；按 rune 覆盖选择 primary/fallback，按相同字体/方向分段整形。
- fontconfig 只返回路径，Go 读取并缓存字体数据；RegisterFont 仍优先作为命名 primary。

- [ ] **Step 1: 写失败测试** `TestHarfBuzzShapesRealScripts`、`TestComplexTextCaretClusters`、`TestFontFallbackDiscoveryAndLifecycle`、`TestNoCGOLegacyFallback`，使用系统 Source Han Sans/DejaVu 等实际存在字体并显式记录环境。
- [ ] **Step 2: 运行 `go test ./internal/text -run 'Test(HarfBuzz|ComplexText|FontFallback|NoCGO)' -v`，确认失败。**
- [ ] **Step 3: 实现 C 资源封装、HarfBuzz shape、FreeType glyph-ID raster、fontconfig 回退和缓存。** 正确映射 byte cluster 到 rune cluster，处理无效 UTF-8、空文本和负 advance；所有分配/注册失败有回滚。
- [ ] **Step 4: 运行 focused tests、race、vet、gofmt；用 `go test` 验证连字/中文/RTL 字形与 caret，而非只检查函数返回。**

### Task 2: UI 绘制与 cluster 安全编辑集成

**Files:**
- Modify: `ui/input_draw.go`, `ui/textarea_draw.go`, `ui/input.go`, `ui/textarea.go`, `ui/textedit.go`, `ui/composition.go`, `ui/label.go`, `ui/button.go`, `ui/checkbox.go`
- Create: `cmd/wind-text/main.go`, `cmd/wind-text/main_test.go`
- Test: `ui/input_draw_test.go`, `ui/textarea_draw_test.go`, `ui/composition_test.go`

**Interfaces:**
- 所有文本组件通过 Face.Layout 的同一 bitmap/offsets；GL 不改变公共绘制接口。
- 编辑器光标移动、鼠标命中、选择、Backspace/Delete 使用 grapheme cluster 边界；内部 rune 索引仍用于绑定和撤销。
- `cmd/wind-text` 展示中文、连字、组合字符、emoji、阿拉伯文和 mixed direction，并支持真实 Fcitx 输入。

- [ ] **Step 1: 写失败测试** `TestComplexClusterEditingAndHitTesting`、`TestComplexTextSelectionAndUndo`、`TestComplexCompositionAndPassword`、`TestComplexLabelAndTextAreaDrawing`。
- [ ] **Step 2: 运行 `go test ./ui -run 'TestComplex' -v`，确认失败。**
- [ ] **Step 3: 集成 Layout 与 cluster 安全编辑。** 双向文本的 caret/选择遵循逻辑 cluster 顺序与 glyph visual order，不用 rune 中点拆 cluster；placeholder/preedit/password 使用同一栈。
- [ ] **Step 4: 添加真实演示和 bounded smoke，运行 `go test ./...`、race、vet、gofmt。** 真实 IME/视觉验证单独记录，不用协议成功代替字形检查。
