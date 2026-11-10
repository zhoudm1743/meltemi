# Frameless Window and Custom Title Bar Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在现有 Go/GLFW/OpenGL 底座上实现无边框窗口、可配置自绘标题栏、窗口控制按钮、标题栏拖动/双击和四边四角缩放。

**Architecture:** 驱动层通过可选 `driver.FrameWindow` 暴露原生窗口控制，避免扩大基础 Window；`internal/windowframe` 用纯 Go 实现布局、样式和交互状态机；新增独立 `cmd/wind-frameless` 完成真实 OpenGL 绘制与端到端验证，不改阶段 0 演示。

**Tech Stack:** Go 1.26、cgo、既有 GLFW 3.3、OpenGL 3.3 Core、`golang.org/x/image`；不新增第三方依赖。

**Spec:** `docs/superpowers/specs/2026-11-03-frameless-titlebar-design.md`

## Global Constraints

- 不使用 Fyne、WebView、HTML 或 JavaScript，不新增依赖。
- 默认窗口必须保留系统装饰；只有 `driver.Config.Frameless == true` 时禁用装饰。
- 不扩大 `driver.Window`；窗口控制通过可选 `driver.FrameWindow` 提供。
- 所有窗口控制、GL 调用和 UI 回调只在 App.Run 锁定的 UI 线程执行。
- 没有输入、状态或尺寸变化时不持续重绘。
- Renderer.Close 只能在目标 context 已成功 MakeCurrent 后调用；失败不得释放 GL、删除状态或销毁窗口。
- 本轮不实现 CSS、透明圆角、阴影或 Windows/macOS 实际适配。
- 当前目录不是 Git 仓库：不得自动 `git init`；提交步骤仅在用户明确建立仓库并授权后执行。
- 不使用 sudo，不修改系统包。

## Review Focus

- 一个无边框窗口后再创建默认窗口：后一窗口必须仍有装饰，GLFW hint 不得串扰；由 Task 1 的 `TestDecorationHintDoesNotLeak` 固定。
- 按钮按下后在按钮外释放：不得触发最小化、最大化或关闭；由 Task 2 的 `TestButtonReleaseOutsideCancels` 固定。
- 左边/上边缩放到最小尺寸以下：尺寸必须被钳制，位置按实际钳制量调整；由 Task 2 的 `TestResizeClampsMinimumAndPosition` 固定。
- 标题栏单击拖动与 300ms 内双击：状态必须区分并在释放后复位；由 Task 2 的 `TestTitleDragAndDoubleClick` 固定。
- 关闭时 MakeCurrent 失败：不得误删另一个 context 的 GL 对象；由 Task 3 的 `TestCloseWindowMakeCurrentFailurePreservesState` 固定。

---

### Task 1: 无边框配置与原生窗口控制

**Files:**
- Modify: `internal/driver/types.go`
- Modify: `internal/driver/driver.go`
- Modify: `internal/driver/config_test.go`
- Modify: `internal/driver/glfw/backend.go`
- Modify: `internal/driver/glfw/window.go`
- Modify: `internal/driver/glfw/events.go`
- Modify: `internal/driver/glfw/events_test.go`
- Test: `internal/driver/glfw/controls_test.go`

**Interfaces:**
- Consumes: 既有 `driver.Config`、`driver.Window`、GLFW Window。
- Produces: `Config.Frameless`、`driver.FrameWindow`、带 Position 的鼠标按钮事件，以及 Task 2 使用的窗口控制方法。

- [ ] **Step 1: 写失败的配置、鼠标位置和控制测试**

`TestDefaultConfigRemainsDecorated` 断言 `DefaultConfig().Frameless == false`。`TestMouseButtonIncludesCursorPosition` 断言按下和释放事件的 Position 等于当前光标位置，而不是零值。

新增 `TestFrameWindowControls` 和 `TestDecorationHintDoesNotLeak`：

```go
type FrameWindow interface {
    Window
    SetSize(Size) error
    Position() Point
    SetPosition(Point)
    Iconify()
    Maximize()
    Restore()
    IsMaximized() bool
}
```

控制测试创建 `Frameless: true` 窗口并断言满足 FrameWindow；SetSize 后 Size 为新值，SetPosition 后 Position 为新值，Iconify/Maximize/Restore 可调用。随后创建 `Frameless == false` 的默认窗口，验证装饰 hint 已恢复；不得以异步 WM 状态作为即时断言。

- [ ] **Step 2: 运行测试并确认失败**

Run: `go test ./internal/driver ./internal/driver/glfw -run 'TestDefaultConfigRemainsDecorated|TestMouseButtonIncludesCursorPosition|TestFrameWindowControls|TestDecorationHintDoesNotLeak' -v`
Expected: FAIL，Frameless、FrameWindow 或鼠标 Position 尚未实现。

- [ ] **Step 3: 实现配置、控制接口和 GLFW 方法**

在 Config 添加 `Frameless bool`，不改变 DefaultConfig 其他值。每次 NewWindow 都显式设置 `Decorated` hint：Frameless 时为 False，否则为 True，然后设置既有 OpenGL hints。

GLFW Window 实现 FrameWindow；Position 使用 GetPos/SetPos 的整数屏幕坐标与 Point 的 float64 互转。SetSize 对非正尺寸返回错误，对已销毁窗口返回 errDestroyed；其他控制方法在销毁后安全 no-op/零值。IsMaximized 使用对应 GLFW attrib。

鼠标按钮回调在 emit 前调用 GetCursorPos，将结果填入 Event.Position；保持侧键丢弃和 Rune/Key 分离行为。

- [ ] **Step 4: 运行驱动测试和全量回归**

Run:

```bash
go test ./internal/driver ./internal/driver/glfw -v
go test ./...
```

Expected: PASS，无遗留窗口；现有 App 和阶段 0 测试不需要修改。

- [ ] **Step 5: 提交（仅已明确授权时）**

```bash
git add internal/driver
git commit -m "feat: add frameless native window controls"
```

### Task 2: 自绘标题栏布局与交互状态机

**Files:**
- Create: `internal/windowframe/style.go`
- Create: `internal/windowframe/layout.go`
- Create: `internal/windowframe/controller.go`
- Test: `internal/windowframe/layout_test.go`
- Test: `internal/windowframe/controller_test.go`

**Interfaces:**
- Consumes: Task 1 的 `driver.FrameWindow`、`driver.Event`、`driver.Point`、`driver.Size`；既有 `render.Rect`、`render.Color`。
- Produces: `Style`、`DefaultStyle`、`Layout`、`Button`、`Controller`、`Result`，供 Task 3 绘制和处理输入。

- [ ] **Step 1: 写失败的布局和命中测试**

固定 `DefaultStyle()`：Height=36、ButtonWidth=46、ResizeMargin=5、MinSize=240x120。布局包含 TitleBar、Icon、Title、Minimize、Maximize、Close 六个 Rect；按钮位于标题栏右侧且互不重叠，图标和标题不进入按钮区域。

测试 `TestButtonHitPriority`、`TestResizeEdgesAndCorners`、`TestButtonReleaseOutsideCancels`、`TestResizeClampsMinimumAndPosition`、`TestTitleDragAndDoubleClick`。四角返回组合 Edges，按钮区域命中按钮而非 Top/Right 缩放。

- [ ] **Step 2: 运行测试并确认失败**

Run: `go test ./internal/windowframe -v`
Expected: FAIL，包和类型尚不存在。

- [ ] **Step 3: 实现 Style、Layout 和 Button/Edges**

```go
type Button uint8
const (
    ButtonNone Button = iota
    ButtonMinimize
    ButtonMaximize
    ButtonClose
)

type Edges uint8
const (
    EdgeLeft Edges = 1 << iota
    EdgeRight
    EdgeTop
    EdgeBottom
)

type Style struct {
    Height, ButtonWidth, ResizeMargin float64
    MinSize driver.Size
    Background, TitleColor, IconColor render.Color
    ButtonColor, ButtonHover, ButtonActive render.Color
    CloseColor, CloseHover, CloseActive render.Color
}
func DefaultStyle() Style
func (s Style) Layout(size driver.Size) Layout
func (l Layout) ButtonAt(p driver.Point) Button
func (s Style) ResizeEdges(size driver.Size, p driver.Point) Edges
```

颜色提供完整默认值；本轮只通过 Style 配置，不解析 CSS。

- [ ] **Step 4: 实现 Controller 和交互测试**

```go
type Result struct { Redraw, Close bool }
func NewController(style Style) *Controller
func (c *Controller) Hover() Button
func (c *Controller) Active() Button
func (c *Controller) HandleEvent(e driver.Event, w driver.FrameWindow, screen driver.Point, now time.Time) Result
```

语义：左键按下记录按钮/拖动/缩放手势；移动更新 hover 并执行拖动或缩放；释放只有在同一按钮内才执行。最小化调用 Iconify，最大化根据 IsMaximized 调 Maximize/Restore，关闭只返回 `Close=true`，由应用释放资源。

标题栏 300ms 内第二次左键按下切换最大化，不启动拖动。拖动位置 = 起始窗口 Position + screen 差值。缩放根据 Edges 修改 Size 和必要的 Position，钳制 MinSize 后用实际尺寸重新计算左/上位置。手势结束清空 active/drag/resize 状态。

- [ ] **Step 5: 运行窗口框架测试和全量回归**

Run:

```bash
go test ./internal/windowframe -v
go test ./...
```

Expected: PASS，覆盖按钮外释放、四角、最小尺寸、双击和拖动。

- [ ] **Step 6: 提交（仅已明确授权时）**

```bash
git add internal/windowframe
git commit -m "feat: add custom window frame interactions"
```

### Task 3: OpenGL 自绘标题栏与真实无边框演示

**Files:**
- Create: `cmd/wind-frameless/main.go`
- Create: `cmd/wind-frameless/render.go`
- Create: `cmd/wind-frameless/smoke.go`
- Test: `cmd/wind-frameless/main_test.go`

**Interfaces:**
- Consumes: Task 1 的 FrameWindow；Task 2 的 Style/Layout/Controller/Result；既有 App、Renderer、Face。
- Produces: 可人工运行的 `wind-frameless` 和自动操作后退出的 `--smoke`；测试入口为 `screenPoint(w driver.FrameWindow, p driver.Point) driver.Point` 与 `runSmokeScript(d *demo) error`。

- [ ] **Step 1: 写失败的集成辅助测试**

`TestScreenPoint` 断言窗口 Position=(100,50) 加事件 Position=(12,8) 得到 screen=(112,58)。`TestCloseWindowMakeCurrentFailurePreservesState` 使用记录式 renderer/target，断言失败时不 Close、不 Destroy、不删状态。

`TestSmokeScriptUsesTitleBarControls` 用假 FrameWindow 验证 smoke 脚本经过 Controller 执行拖动、缩放和最大化，而不是只调用原生方法绕过自绘交互。

- [ ] **Step 2: 运行测试并确认失败**

Run: `go test ./cmd/wind-frameless -v`
Expected: FAIL，命令和辅助函数尚不存在。

- [ ] **Step 3: 实现无边框演示和资源生命周期**

创建一个 `Frameless: true`、720x480 窗口，建立 App、Renderer、Face 和 Task 2 Controller。标题栏使用 Style/Layout 绘制：左侧 16x16 图标和标题文字，右侧最小化、最大化、关闭按钮；按钮使用 Hover/Active 对应颜色，正文包含矩形和说明文字。图形符号优先用矩形/简单文字，不引入图片依赖。

事件处理：用窗口 Position + 事件 Position 构造 screen 坐标并传给 Controller；Result.Redraw 时重绘，Result.Close 时走统一 CloseWindow。原生 CloseRequested 和 Escape 也走统一关闭。Resize/FramebufferResize/Paint 按需重绘。

CloseWindow 必须先成功 MakeCurrent，再 Close Renderer、删除状态、关闭 Face、App.CloseWindow；失败立即返回并完整保留状态。初始化失败按逆序 best-effort 清理。

- [ ] **Step 4: 实现真实 smoke 脚本**

`runSmokeScript(d *demo) error` 在窗口建立后通过 Posted 函数执行，不在定时 goroutine 直接操作 UI：

1. 通过 Controller 事件在标题栏按下、移动、释放，断言窗口 Position 改变。
2. 通过 Controller 事件从右下角缩放，断言 Size 增大且不小于 MinSize。
3. 通过最大化按钮事件切换 Maximize/Restore。
4. 调用 Iconify/Restore 验证最小化控制路径。
5. 通过关闭按钮事件触发统一 CloseWindow，全部完成后 Quit。

任一步骤失败都记录首个错误并以非零码退出；即使关闭或清理失败也必须调用 Quit，避免无人值守卡死。`TestSmokeScriptUsesTitleBarControls` 直接针对该函数验证调用路径。

- [ ] **Step 5: 运行完整标准验证**

Run:

```bash
go test ./...
go test -race ./internal/app ./internal/driver/fake ./internal/windowframe ./cmd/wind-frameless
go vet ./...
go build ./cmd/wind-frameless
go run ./cmd/wind-frameless --smoke
```

Expected: 全部退出码 0；真实 Linux 窗口完成脚本后自动退出，无 panic、cgo 错误或残留进程。删除 `wind-frameless` 构建产物。

- [ ] **Step 6: 验证真实桌面观感**

Run: `go run ./cmd/wind-frameless`

Expected: 没有系统标题栏；图标、标题、按钮可见，hover/按下样式改变，拖动、双击、缩放、最小化、最大化/还原和关闭符合设计。无法真人验证的部分必须明确报告，不以 smoke 代替。

- [ ] **Step 7: 提交（仅已明确授权时）**

```bash
git add cmd/wind-frameless
git commit -m "feat: add frameless window with custom title bar"
```
