# Stage 2 High DPI and Input Completion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 文本按物理像素清晰光栅化，同时保留逻辑坐标布局；补齐常见桌面词级/文档/分页编辑和可观察的剪贴板错误。

**Architecture:** renderer 已知 framebuffer/logical 比例，文本 face 按物理字号生成 coverage bitmap，bitmap 同时携带物理尺寸和逻辑绘制尺寸。组件布局仍用逻辑单位。编辑操作继续由 shared textBuffer/multiTextBuffer 实现，避免 Input/TextArea 行为分叉。

**Tech Stack:** Go 1.26、既有 x/image、GLFW/OpenGL；不新增依赖。

**Spec:** `doc/总体设计.md` 第 3.1、5.5、10、11、13 阶段2、15节。

## Global Constraints

- 公共 API 不暴露内部 driver/render/字体类型；CSS px 始终为逻辑单位。
- 非整数缩放、framebuffer resize 和零尺寸必须安全；高 DPI 不得改变组件逻辑布局。
- 所有编辑/回调仍在 UI 线程；IME composition 不被词级快捷键破坏。
- 不实现 Stage 3 平台或 Stage 4 无障碍/动画。
- 非 Git 原地工作区，不初始化/提交。

## Review Focus

- 2x/1.5x 和 resize：bitmap 物理像素变化、目标矩形仍为逻辑尺寸；Task 1 的 `TestTextPhysicalRasterizationAndResize` 固定。
- 组合输入中的 Ctrl+Arrow/Backspace：不得误删预编辑或触发菜单；Task 2 的 `TestWordEditingDuringComposition` 固定。
- 剪贴板错误：文本保持不变且回调只执行一次；Task 2 的 `TestEditorClipboardErrorCallback` 固定。

---

### Task 1: 物理像素文本光栅化

**Files:**
- Modify: `internal/text/face.go`, `internal/text/face_test.go`, `internal/render/gl/text.go`, `internal/render/gl/text_test.go`, `internal/render/gl/renderer.go`

**Interfaces:**
- 内部 `text.Face.RasterizeScaled(value string, scaleX, scaleY float64) (Bitmap,error)`；Bitmap 增加物理像素与逻辑尺寸/原点信息，旧 `Rasterize` 等价于 scale 1。
- Face 缓存派生物理 face 并在 Close 逆序释放；GL DrawText 根据当前 target 比例调用，不修改组件测量接口。

- [ ] **Step 1: 写失败测试** `TestRasterizeScaledBitmapGeometry`、`TestTextPhysicalRasterizationAndResize`、`TestScaledFaceCacheLifecycle`，覆盖 1x/1.5x/2x、非法 scale、空文本和非零 origin。
- [ ] **Step 2: 运行 `go test ./internal/text ./internal/render/gl -run 'Test(RasterizeScaled|TextPhysical|ScaledFace)' -v`，确认失败。**
- [ ] **Step 3: 实现物理 face/位图与 GL 目标矩形分离。** 物理 alpha 上传不得误用逻辑宽高；resize 后重新生成对应物理位图。
- [ ] **Step 4: 运行 focused 和全量测试；用真实 GL 检查像素，不用拉伸逻辑位图冒充高 DPI。**

### Task 2: 桌面编辑行为与错误回调

**Files:**
- Modify: `ui/textedit.go`, `ui/multiedit.go`, `ui/input.go`, `ui/textarea.go`, `ui/event.go`, `internal/driver/types.go`, `internal/driver/glfw/events.go`
- Test: `ui/textedit_test.go`, `ui/multiedit_test.go`, `ui/input_test.go`, `ui/textarea_test.go`, `internal/driver/glfw/events_test.go`

**Interfaces:**
- textBuffer 增加词级 `MoveWord(delta,extend)`、`BackspaceWord()`、`DeleteWord()`；multiTextBuffer 增加文档 Home/End 和分页移动。
- Input/TextArea 增加 `OnError(func(error))`；clipboard 失败只报告，不修改文本，不使 App 全局退出。
- Ctrl+Left/Right、Ctrl+Backspace/Delete；TextArea 另支持 Ctrl+Home/End 和 PageUp/PageDown。修饰键不匹配时保持原行为。

- [ ] **Step 1: 写失败测试** `TestWordNavigationAndDeletion`、`TestDocumentAndPageNavigation`、`TestEditorClipboardErrorCallback`、`TestWordEditingDuringComposition`，覆盖中文/英文混排、连续空白、边界、选择扩展和撤销。
- [ ] **Step 2: 运行 `go test ./ui ./internal/driver/glfw -run 'Test(Word|Document|EditorClipboard|Page)' -v`，确认失败。**
- [ ] **Step 3: 实现共享编辑算法和按键映射。** 词边界使用 Unicode letter/digit 与空白分组，不依赖英文 ASCII；错误回调在状态未变时调用且可重入安全。
- [ ] **Step 4: 运行 focused、`go test ./...`、race、vet 和 gofmt；更新 cmd/wind-notes 演示以覆盖新行为。**
