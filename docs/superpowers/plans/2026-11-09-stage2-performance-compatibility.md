# Stage 2 Performance and Compatibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 消除重复文本光栅化/纹理上传，提供可测量缓存证据，并记录 Linux 菜单、托盘、输入法、文件对话框的真实支持矩阵和未验证边界。

**Architecture:** 每个 GL renderer 持有有界文本纹理 LRU，key 包含 Face、文本、物理缩放；颜色走 uniform 不进纹理 key。缓存只在正确 context 下执行 GL 操作。兼容性矩阵只记录实际证据，不用设计目标替代测试。

**Tech Stack:** Go 1.26、既有 OpenGL/HarfBuzz/desktop 服务；不新增依赖。

**Spec:** `doc/总体设计.md` 第 4、11.1、12、13 阶段2、15节。

## Global Constraints

- 缓存按 renderer/context 隔离，Close/Discard/替换 Face/scale 行为正确。
- 缓存有固定容量和确定性 LRU 淘汰；错误不得缓存为成功。
- 无变化时不持续满帧重绘；不引入后台绘制。
- 不把未运行的平台/桌面/IME 标为通过。
- 非 Git 原地工作区，不初始化/提交。

## Review Focus

- 同文本不同颜色：复用纹理但颜色正确；Task 1 的 `TestTextCacheReusesCoverageNotColor` 固定。
- 多窗口/scale/Face 替换与 LRU 淘汰：不得串 texture 或误删其他 context 资源；Task 1 的 `TestTextCacheIsolationAndEviction` 固定。
- 性能测量：比较相同输入的 cold/warm 调用与上传次数，不宣称未经测量的提升；Task 1 的 benchmark/test 固定。

---

### Task 1: 有界文本纹理缓存与性能测量

**Files:**
- Create: `internal/render/gl/text_cache.go`, `internal/render/gl/text_cache_test.go`, `internal/render/gl/text_benchmark_test.go`
- Modify: `internal/render/gl/text.go`, `internal/render/gl/text_test.go`, `internal/render/gl/renderer.go`

**Interfaces:**
- 内部 cache key = face pointer + value + physical scaleX/Y；每 renderer 默认最多 256 个 coverage textures。
- 内部测试可读取 hit/miss/upload/eviction counters；不新增公共调试 API。
- 文本透明度和颜色仍每帧应用；空文本不分配资源，错误结果不进缓存。

- [ ] **Step 1: 写失败测试** `TestTextCacheReusesCoverageNotColor`、`TestTextCacheIsolationAndEviction`、`TestTextCacheCloseAndDiscard`、`TestTextCacheScaleAndFaceInvalidation`，并添加相同文本 cold/warm benchmark。
- [ ] **Step 2: 运行 `go test ./internal/render/gl -run 'TestTextCache' -v`，确认失败。**
- [ ] **Step 3: 实现有界 LRU、计数和 Close/Discard。** 淘汰/关闭仅在对应 context current 时调用 GL；多 renderer 不共享 texture 名。
- [ ] **Step 4: 运行 focused、`go test ./...`、race、vet、gofmt 和 `go test ./internal/render/gl -run '^$' -bench 'TextCache' -benchmem`，记录实际数值和机器环境。**

### Task 2: 兼容矩阵与阶段2整体验证

**Files:**
- Create: `doc/兼容性.md`
- Modify: `doc/总体设计.md`（仅同步阶段2已变化的支持边界）
- Test: 既有真实 demos/smoke

**Interfaces:**
- 矩阵覆盖 DDE/X11、Fcitx5、XDG portal、StatusNotifierItem/dbusmenu、OpenGL/HarfBuzz/FreeType/fontconfig；每项分自动化、协议 smoke、视觉/人工和未验证。
- 明确 GNOME/KDE/Wayland、其他 IME、Windows/macOS 未验证，不以 Linux 实现推断兼容。

- [ ] **Step 1: 汇总可复核证据和命令。** 对照三阶段计划及报告，记录真实 DDE 的菜单/托盘/文件对话框/文本/IME 状态；锁屏阻断项保持未验证。
- [ ] **Step 2: 编写矩阵和已知限制。** 不创建泛泛说明，只写支持组合、验证命令、证据等级和限制。
- [ ] **Step 3: 运行最终 `go test ./... -count=1`、核心 race、vet、gofmt、layout/menu/tray/dialog/text smoke。** 新文件/进程/二进制检查；失败必须保留并列入矩阵。
- [ ] **Step 4: 阶段2整体代码审查。** 复核公共边界、C 资源、D-Bus 并发、编辑回归、缓存生命周期与证据完整性。
