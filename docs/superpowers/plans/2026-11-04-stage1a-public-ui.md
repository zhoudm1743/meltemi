# Stage 1A Public Go UI API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增公开 `wind/ui` 包，以链式 Go API 创建多窗口、Column、Label 和 Button，并支持鼠标、键盘、关闭策略及真实 OpenGL 演示。

**Architecture:** 公开 Component 是只允许内置组件组合的封闭接口，内部组件实现测量、排列、绘制和事件契约，暂不公开任意自绘 API。公开 App/Window 包装既有内部事件循环和 GLFW/OpenGL，每个窗口拥有独立 renderer 和事件路由；Column/Label/Button 与后端保持可单测。

**Tech Stack:** Go 1.26、既有内部 App/GLFW/OpenGL/text/windowframe；不新增第三方依赖。

**Spec:** `doc/总体设计.md` 的第 3、5、6、8、10、13、14、15 节，以及已完成的 `docs/superpowers/specs/2026-11-03-frameless-titlebar-design.md`。

## Global Constraints

- 不使用 Fyne、WebView、HTML 或 JavaScript，不新增依赖。
- 公共 API 不得暴露 GLFW/OpenGL 类型，不得要求应用处理内部 driver/render 类型。
- 本阶段只新增公开 `ui` 包和 `cmd/wind-ui-basic`；不实现 CSS、Input、菜单、托盘或公开任意 Canvas。
- 所有 UI 状态和回调只在 UI 线程；工作 goroutine 只能通过 `App.Post` 更新。
- 默认窗口有装饰，`Frameless(true)` 使用既有无边框能力；默认关闭操作为 Destroy，默认最后窗口销毁后 Quit，均可配置。
- Renderer.Close 只能在对应 context 成功 MakeCurrent 后调用；失败不得误删其他 context 的对象。
- 首帧或初始化失败必须返回首个错误并按逆序 best-effort 清理。
- 当前非 Git 仓库，不自动初始化或提交；不使用 sudo。

## Review Focus

- 空 Column 和单子组件：测量不得产生负 gap，Padding 必须准确计算；由 Task 1 的 `TestColumnEmptyAndSingleChild` 固定。
- Disabled Button 或按下后在外部释放：不得调用 OnClick；由 Task 2 的 `TestButtonDisabledAndReleaseOutside` 固定。
- OnClose veto、CloseHide 和 LastWindowKeepRunning：不得销毁窗口或退出；由 Task 3 的 `TestWindowClosePolicies` 固定。
- 多窗口中关闭一个：另一个的 renderer、Face 和事件路由必须保持可用；由 Task 3 的 `TestCloseOneOfTwoWindows` 固定。
- 首帧 Begin/DrawText/End 失败：必须传播首错并逆序释放资源；由 Task 3 的 `TestFirstFrameFailureCleansUp` 固定。

---

### Task 1: 组件契约与 Column 布局

**Files:**
- Create: `ui/component.go`
- Create: `ui/layout.go`
- Create: `ui/column.go`
- Test: `ui/column_test.go`

**Interfaces:**
- Consumes: 既有 `render.Rect`、`driver.Event` 和文本绘制能力。
- Produces: 公开 `Component`、`Constraints`，以及内部 `component`、drawContext 和 `Column`。

- [ ] **Step 1: 写失败的 Column 测试**

使用固定自然尺寸的 fake component，覆盖：

```go
func TestColumnMeasureAndArrange(t *testing.T)  // Padding=20、Gap=12、两个子组件高度 24/36。
func TestColumnEmptyAndSingleChild(t *testing.T) // 空内容只有 Padding；单 child 无 gap。
func TestColumnNested(t *testing.T)             // 嵌套 Column 的测量与绘制位置。
```

两个子组件时，内容宽取最大 child 宽，内容高为 child 高之和 + 12；总尺寸再加双倍 Padding。Arrange 水平拉伸至内容宽，垂直保持自然高度并自上而下排列。

- [ ] **Step 2: 运行测试并确认失败**

Run: `go test ./ui -run TestColumn -v`
Expected: FAIL，公开包和类型尚不存在。

- [ ] **Step 3: 实现公开 Component 与内部组件契约**

```go
type Component interface { isUIComponent() }

type Constraints struct { Min, Max Size }
type Size struct { Width, Height float64 }

type component interface {
    Component
    measure(Constraints) Size
    arrange(render.Rect)
    draw(*drawContext)
    handleEvent(driver.Event) (handled, redraw bool)
    walk(func(component))
}
```

`Constraints.Max` 的零维度表示不限；measure 结果必须被 Min/Max 钳制。`drawContext` 是内部渲染与字体上下文，不暴露给应用开发者。外部可返回和组合内置 Component，但不能在本阶段自行实现任意绘制组件。

- [ ] **Step 4: 实现 Column 链式布局和测试**

```go
func Column(children ...Component) *Column
func (c *Column) Gap(value float64) *Column
func (c *Column) Padding(value float64) *Column
```

Gap/Padding 负值钳为 0。Column 的 handleEvent 按组件树顺序转发并汇总 redraw，walk 遍历自身和所有后代；默认绘制只绘制子组件。

- [ ] **Step 5: 运行组件布局测试**

Run: `go test ./ui -run TestColumn -v`
Expected: PASS。

- [ ] **Step 6: 提交（仅已明确授权时）**

```bash
git add ui
git commit -m "feat: add component model and column layout"
```

### Task 2: Label、Button 与鼠标键盘事件路由

**Files:**
- Create: `ui/theme.go`
- Create: `ui/label.go`
- Create: `ui/button.go`
- Create: `ui/event.go`
- Test: `ui/label_test.go`
- Test: `ui/button_test.go`
- Test: `ui/event_test.go`

**Interfaces:**
- Consumes: Task 1 的 component/drawContext、Column、driver.Event；既有 text.Face。
- Produces: `Label`、`Button`、`eventRouter`，供 Task 3 窗口使用。

- [ ] **Step 1: 写失败的组件和事件测试**

覆盖：

```go
func TestLabelSetTextInvalidatesMeasurement(t *testing.T)
func TestButtonMeasureAndClick(t *testing.T)
func TestButtonDisabledAndReleaseOutside(t *testing.T)
func TestButtonHoverAndCursorLeave(t *testing.T)
func TestKeyboardFocusAndEnter(t *testing.T)
```

Button 默认高 36、水平内边距 16，文本尺寸来自 Face.Rasterize；点击必须经历“内部左键按下 → 内部释放”，Disabled 或外部释放不触发。Tab/Shift+Tab 按组件树顺序循环焦点，Enter 激活焦点 Button，EventCursorLeave 只清 hover 不取消 pressed。

- [ ] **Step 2: 运行测试并确认失败**

Run: `go test ./ui -run 'TestLabel|TestButton|TestKeyboard' -v`
Expected: FAIL，组件与路由尚未实现。

- [ ] **Step 3: 实现默认主题、Label 和 Button**

```go
func Label(text string) *Label
func (l *Label) Text() string
func (l *Label) SetText(text string)

func Button(text string) *Button
func (b *Button) Text() string
func (b *Button) SetText(text string)
func (b *Button) Enabled() bool
func (b *Button) SetEnabled(enabled bool)
func (b *Button) OnClick(fn func()) *Button
func (b *Button) Click()
```

默认主题使用 GoRegular 18px，并定义 Label 文字、Button 背景/hover/active/disabled、焦点边框和文字颜色。Button Click 在 Enabled 且回调非空时执行；SetText/SetEnabled 更新内部状态，由 Task 3 负责重绘。

- [ ] **Step 4: 实现 eventRouter 和焦点行为**

```go
type eventRouter struct { /* root and focused component */ }
func newEventRouter(root component) *eventRouter
func (r *eventRouter) Handle(e driver.Event) (handled, redraw bool)
```

路由将鼠标事件交给组件，管理焦点、Tab、Shift+Tab、Enter 和离窗 hover。事件只路由一次；被消费的点击不得再触发其他 Button。 Disabled 组件不能获得焦点。

- [ ] **Step 5: 运行组件与事件测试**

Run: `go test ./ui -run 'TestLabel|TestButton|TestKeyboard' -v`
Expected: PASS。

- [ ] **Step 6: 提交（仅已明确授权时）**

```bash
git add ui
git commit -m "feat: add labels buttons and event routing"
```

### Task 3: 公开 App/Window、关闭策略与资源所有权

**Files:**
- Create: `ui/app.go`
- Create: `ui/window.go`
- Create: `ui/renderer.go`
- Test: `ui/app_test.go`
- Test: `ui/window_test.go`

**Interfaces:**
- Consumes: 前两个任务的 component/eventRouter；既有 internal/app、driver/fake、render.Target、GLFW/OpenGL/text。
- Produces: 公开 `App`、`Window` 和关闭策略 API，供 Task 4 及真实应用使用。

- [ ] **Step 1: 写失败的 App/Window 测试**

使用 fake backend、fake render target 和可注入 renderer，覆盖：

```go
func TestWindowShowContentAndPost(t *testing.T)
func TestWindowClosePolicies(t *testing.T)
func TestCloseOneOfTwoWindows(t *testing.T)
func TestFirstFrameFailureCleansUp(t *testing.T)
func TestWindowFramelessConfiguration(t *testing.T)
```

ClosePolicies 必须分别验证：OnClose 返回 false 取消；CloseHide 只隐藏；LastWindowKeepRunning 在最后窗口销毁后不退出；默认 LastWindowQuit 退出。

- [ ] **Step 2: 运行测试并确认失败**

Run: `go test ./ui -run 'TestWindow|TestClose|TestFirstFrame' -v`
Expected: FAIL，App/Window 尚未实现。

- [ ] **Step 3: 实现公开 App 和链式 Window**

```go
func New() *App
func (a *App) Run() error
func (a *App) Quit()
func (a *App) Post(fn func()) error
func (a *App) Window(title string) *Window
func (a *App) Windows() []*Window
func (a *App) SetLastWindowAction(action LastWindowAction)

type LastWindowAction uint8
const (
    LastWindowQuit LastWindowAction = iota
    LastWindowKeepRunning
)

type CloseAction uint8
const (
    CloseDestroy CloseAction = iota
    CloseHide
)

func (w *Window) Size(width, height int) *Window
func (w *Window) Frameless(frameless bool) *Window
func (w *Window) Content(component Component) *Window
func (w *Window) CloseAction(action CloseAction) *Window
func (w *Window) OnClose(fn func() bool) *Window
func (w *Window) Show()
func (w *Window) Hide()
func (w *Window) Close() error
```

配置方法在窗口 Show 前设置；Show/Hide/Close 和 Content 更新通过 App.Post 进入 UI 线程。App.Post 在 UI 函数返回后对现有窗口统一标记失效并重绘，保证 Label.SetText、Button.SetEnabled 等更新不需要额外通知；这不改变事件驱动渲染，禁止后台持续重绘。公开 Windows 返回副本。OnClose 返回 false 取消原生关闭请求；CloseHide 不销毁 renderer/native window。最后窗口策略只在实际 Destroy 后评估。

- [ ] **Step 4: 实现 renderer 生命周期、首帧和关闭清理**

每窗口独立 renderer/eventRouter；Face 可由 App 共享，并在最后一个窗口关闭后释放；LastWindowKeepRunning 后新建窗口时必须能按需重新创建 Face。首帧执行 measure、arrange、draw，Begin/DrawText/End 错误按首错返回并清理。关闭时先 MakeCurrent，再 Close renderer、移除状态、销毁 native；失败完整保留。多窗口关闭互不影响，最后无窗口时关闭 Face。

- [ ] **Step 5: 运行公开 API 和全量测试**

Run:

```bash
go test ./ui -v
go test ./...
```

Expected: PASS，无真实窗口或 GPU 依赖的单元测试通过。

- [ ] **Step 6: 提交（仅已明确授权时）**

```bash
git add ui
git commit -m "feat: expose app and window lifecycle API"
```

### Task 4: 链式 API 多窗口真实演示

**Files:**
- Create: `cmd/wind-ui-basic/main.go`

**Interfaces:**
- Consumes: Task 1-3 的全部公开 API。
- Produces: 可运行的 `wind-ui-basic` 和 `--smoke` 自动验证。

- [ ] **Step 1: 先运行缺失入口确认 RED**

Run: `go run ./cmd/wind-ui-basic --smoke`
Expected: FAIL，目录或 main 尚不存在。

- [ ] **Step 2: 实现链式多窗口演示**

主窗口代码保持以下形态：Counter Label、Open Settings Button、Column.Gap(12).Padding(20)。点击 Button 后更新计数并创建/显示 Settings 窗口，演示 Label.SetText、Button.Click、App.Post 和多窗口。

两个演示窗口都保留系统装饰，确保具备拖动和关闭入口；公开 Frameless 配置由 Task 3 的单元测试覆盖。演示不得自行调用 GLFW/OpenGL，也不得绕过公开 App/Window 管理 renderer。

- [ ] **Step 3: 实现 smoke 并运行真实端到端验证**

`--smoke` 通过定时器 Post 到 UI 线程：调用主按钮 Click 打开第二窗口、更新计数、检查公开 Windows 数量为 2，再逐个 Close，验证默认最后窗口策略退出。任何错误都记录首个错误并以非零码退出，同时 Quit 防挂死。

Run:

```bash
go test ./...
go test -race ./ui ./internal/app
go vet ./...
go build ./cmd/wind-ui-basic
go run ./cmd/wind-ui-basic --smoke
```

Expected: 全部退出码 0；真实创建两个窗口并自动释放退出，无残留进程。删除构建产物。

- [ ] **Step 4: 验证真实桌面场景**

Run: `go run ./cmd/wind-ui-basic`

Expected: 两个窗口都有系统装饰；按钮 hover/pressed/focus、鼠标点击、Tab/Shift+Tab、Enter 和 Label 更新正常；关闭一个窗口不影响另一个。无法真人或高 DPI 验证必须明确披露。

- [ ] **Step 5: 提交（仅已明确授权时）**

```bash
git add cmd/wind-ui-basic
git commit -m "feat: add chained public UI API demo"
```
