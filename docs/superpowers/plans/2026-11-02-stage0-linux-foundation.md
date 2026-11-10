# Stage 0 Linux Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 Linux/X11 上跑通两个原生窗口、OpenGL 图元与文字绘制、输入事件、跨 goroutine 投递和有序退出，为后续组件与样式系统提供内部底座。

**Architecture:** 以 `internal/driver` 定义与 GLFW 无关的窗口和事件契约，`internal/app` 管理唯一 UI 事件循环；GLFW 和 OpenGL 分别实现窗口后端与渲染器。阶段 0 只提供内部 API 和 `cmd/wind-stage0` 验证程序，不公开或冻结组件、Canvas 与布局 API。

**Tech Stack:** Go 1.26、cgo、GLFW 3.3、OpenGL 3.3 Core、`golang.org/x/image/font` 基础文字光栅化。

**Spec:** `doc/总体设计.md` 的阶段 0，以及第 5.5、10、11、15 节。

## Global Constraints

- 不使用 Fyne、WebView、HTML 或 JavaScript。
- 允许 cgo；不得让组件或应用核心直接依赖 GLFW/OpenGL 类型。
- `go.mod` 使用 `module wind` 和 `go 1.26.0`；当前验证工具链为 Go 1.26.4。
- 固定依赖：`github.com/go-gl/glfw/v3.3/glfw@v0.0.0-20260823155953-d41da22a9587`、`github.com/go-gl/gl@v0.0.0-20260331235117-4566fea9a276`、`golang.org/x/image@v0.47.0`。
- 首版上下文使用 OpenGL 3.3 Core；不得引入持续满帧渲染。
- 所有后端调用和 UI 回调只在 `App.Run()` 锁定的 UI 线程执行；跨 goroutine 只能通过 `App.Post()`。
- 阶段 0 不创建公开 `ui` 包，不实现组件树、CSS、菜单或托盘。
- 当前目录不是 Git 仓库：不得自动 `git init`；所有提交步骤只有在用户明确建立仓库并授权提交后才执行。
- 当前系统缺少 `/usr/include/X11/extensions/xf86vmode.h`。不得自动使用 sudo；需要用户安装 `libxxf86vm-dev` 或明确授权安装等价开发包后，才能执行 GLFW 编译与集成验证。

## Review Focus

- 在 `Run()` 前、运行中及 `Quit()` 后调用 `Post()`：前两种必须执行且不跑在调用 goroutine，最后一种必须返回 `ErrClosed` 并退出等待；由 Task 2 的 `TestPostFromGoroutineWakesRun` 和 `TestPostAfterQuit` 固定。
- 两个窗口中关闭一个：另一个和应用事件循环必须继续存在，退出策略由示例决定；由 Task 2 的 `TestCloseOneOfTwoWindows` 和 Task 6 烟雾场景固定。
- 逻辑尺寸与 framebuffer 尺寸为 1:2：绘制坐标仍使用逻辑单位，viewport 和裁剪必须使用物理像素；由 Task 4 的 `TestClipPixelsHighDPI` 固定。
- 空字符串、非法 UTF-8 和缺失字形：不得 panic，空文本返回空位图，其余情况使用字体回退字形；由 Task 5 的文本测试固定。
- 后端初始化或窗口创建失败：必须返回错误、停止事件循环并释放已初始化的全局资源；由 Task 2 的 `TestInitFailure` 和 Task 3 的 `TestCreateWindowFailureCleansUp` 固定。

---

### Task 1: 模块与窗口驱动契约

**Files:**
- Create: `go.mod`
- Create: `internal/driver/types.go`
- Create: `internal/driver/driver.go`
- Test: `internal/driver/config_test.go`

**Interfaces:**
- Consumes: 无。
- Produces: `Size`、`Point`、`Config`、`Event`、`Window`、`Backend` 以及后续任务使用的事件类型。

- [ ] **Step 1: 验证 Linux 编译前置条件**

Run:

```bash
pkg-config --exists x11 xcursor xi xinerama xrandr xxf86vm gl
```

Expected: 安装缺失的 Xxf86vm 开发文件后退出码为 0。当前环境预期仅 `xxf86vm` 失败；若仍失败，停止 GLFW 任务并报告缺失包，不绕过系统依赖。

- [ ] **Step 2: 写失败的配置测试**

```go
func TestConfigValidate(t *testing.T) {
    if err := DefaultConfig().Validate(); err != nil { t.Fatal(err) }
    for _, size := range []Size{{0, 600}, {800, 0}, {-1, 600}} {
        if err := (Config{Title: "bad", Size: size}).Validate(); err == nil {
            t.Fatalf("expected invalid size %v to fail", size)
        }
    }
}
```

同时断言 `DefaultConfig()` 返回 `Title == "Wind"` 和 `Size{Width: 800, Height: 600}`。

- [ ] **Step 3: 运行测试并确认失败**

Run: `go test ./internal/driver`
Expected: FAIL，当前没有 `go.mod` 或 `DefaultConfig` 未定义。

- [ ] **Step 4: 实现模块和驱动契约**

创建 `module wind`、`go 1.26.0`，然后实现：

```go
type Size struct { Width, Height int }
type Point struct { X, Y float64 }
type Config struct { Title string; Size Size }
func DefaultConfig() Config
func (c Config) Validate() error

type EventKind uint8
const (
    EventCloseRequested EventKind = iota + 1
    EventResize
    EventFramebufferResize
    EventPaint
    EventMouseMove
    EventMouseButton
    EventKey
    EventRune
)
```

`Event` 使用 `Window Window`、`Size Size`、`Position Point`、`Button MouseButton`、`Action Action`、`Key Key`、`Mods Modifiers`、`Rune rune` 字段。定义鼠标左/中/右键，按下/释放/重复动作，以及 Unknown、A、Escape、Enter、Tab、Backspace、Delete、Left、Right、Up、Down、Home、End 键和 Shift/Ctrl/Alt/Super 修饰键。

```go
type Handler func(Event)
type Window interface {
    Show()
    Hide()
    Destroy()
    Size() Size
    SetHandler(Handler)
}

type Backend interface {
    Init() error
    Terminate()
    NewWindow(Config) (Window, error)
    PollEvents()
    WaitEvents()
    Wake()
}
```

- [ ] **Step 5: 运行契约测试**

Run: `go test ./internal/driver`
Expected: PASS。

- [ ] **Step 6: 提交（仅已明确授权时）**

```bash
git add go.mod internal/driver
git commit -m "feat: define native window driver contracts"
```

### Task 2: 应用事件循环与多窗口所有权

**Files:**
- Create: `internal/app/app.go`
- Create: `internal/driver/fake/fake.go`
- Test: `internal/app/app_test.go`

**Interfaces:**
- Consumes: Task 1 的 `driver.Backend`、`driver.Window`、`driver.Config`。
- Produces: `app.New`、`Run`、`Post`、`Quit`、`NewWindow`、`CloseWindow`、`Windows` 和 `ErrClosed`。

- [ ] **Step 1: 写失败的事件循环测试**

使用可阻塞在 `WaitEvents()`、能被 `Wake()` 唤醒的假后端，覆盖：

```go
func TestPrepostedTaskRunsBeforeWait(t *testing.T)
func TestPostFromGoroutineWakesRun(t *testing.T)
func TestPostAfterQuit(t *testing.T)
func TestCloseOneOfTwoWindows(t *testing.T)
func TestInitFailure(t *testing.T)
```

关键断言：`Post` 前置于 `Run` 的任务先执行；goroutine 投递能在两秒内完成；`Quit` 后 `Post` 返回 `ErrClosed` 且函数不执行；关闭两个窗口中的一个后 `Windows()` 只剩另一个且应用继续运行；`Init` 的哨兵错误原样返回并调用 `Terminate` 清理。

- [ ] **Step 2: 运行测试并确认失败**

Run: `go test ./internal/app -run 'TestPreposted|TestPost|TestClose|TestInit' -v`
Expected: FAIL，`app.New` 等尚未实现。

- [ ] **Step 3: 实现假后端和应用核心**

假后端记录初始化、终止、窗口创建和销毁次数，`WaitEvents` 只有在任务或唤醒到达时才返回。

```go
var ErrClosed = errors.New("ui: application closed")

type App struct { /* backend, queued funcs, window set, quit state */ }
func New(backend driver.Backend) *App
func (a *App) Run() error
func (a *App) Post(fn func()) error
func (a *App) Quit()
func (a *App) NewWindow(c driver.Config) (driver.Window, error) // 仅 UI 线程
func (a *App) CloseWindow(w driver.Window) error       // 仅 UI 线程
func (a *App) Windows() []driver.Window                // 返回副本，仅 UI 线程
```

`Run` 先 `runtime.LockOSThread()`，再初始化和排空预投递任务；循环中依次处理任务、`PollEvents()`、`WaitEvents()`。`Post` 投递后调用 `Wake()`，不得在调用 goroutine 执行任务。`Run` 返回前销毁剩余窗口并调用 `Terminate()`。不要因为窗口数暂时为零而自动退出。

- [ ] **Step 4: 运行应用核心测试和竞态检测**

Run:

```bash
go test ./internal/app ./internal/driver/fake -v
go test -race ./internal/app
```

Expected: PASS，无死锁、超时或竞态报告。

- [ ] **Step 5: 提交（仅已明确授权时）**

```bash
git add internal/app internal/driver/fake
git commit -m "feat: add multi-window application event loop"
```

### Task 3: GLFW 窗口与输入后端

**Files:**
- Create: `internal/driver/glfw/backend.go`
- Create: `internal/driver/glfw/window.go`
- Create: `internal/driver/glfw/events.go`
- Modify: `go.mod`
- Test: `internal/driver/glfw/events_test.go`
- Test: `internal/driver/glfw/backend_test.go`

**Interfaces:**
- Consumes: Task 1 的全部驱动契约。
- Produces: `glfw.New() *Backend`，其窗口同时满足 Task 4 的 `render.Target`。

- [ ] **Step 1: 写失败的事件映射和失败清理测试**

`TestMapKey` 至少断言 A、Escape、Enter、Tab、方向和 Home/End 的映射；`TestCreateWindowFailureCleansUp` 使用无法创建的非法配置，断言返回错误且没有残留可用窗口。编译期断言：

```go
var _ driver.Backend = (*Backend)(nil)
var _ driver.Window = (*Window)(nil)
```

- [ ] **Step 2: 运行测试并确认失败**

Run: `go test ./internal/driver/glfw -v`
Expected: FAIL，后端和映射函数尚未实现；若因 Xxf86vm 头文件失败，先完成 Global Constraints 中的环境前置条件。

- [ ] **Step 3: 添加固定依赖并实现 GLFW 后端**

Run:

```bash
go get github.com/go-gl/glfw/v3.3/glfw@v0.0.0-20260823155953-d41da22a9587
```

实现 `New() *Backend` 和 Task 1 的 `Backend` 接口。`NewWindow` 校验配置，设置 OpenGL 3.3 Core、ForwardCompatible 提示并创建窗口。回调只转换为 Task 1 事件：关闭请求、逻辑尺寸、framebuffer 尺寸、刷新、鼠标位置/按键、键盘和字符输入；字符输入使用 `EventRune`，不能由 Key 猜测文本。

`Window` 额外实现：

```go
func (w *Window) MakeCurrent() error
func (w *Window) FramebufferSize() driver.Size
func (w *Window) Present()
```

`Destroy` 必须幂等；所有方法除 `Backend.Wake()` 外都只允许 UI 线程调用。

- [ ] **Step 4: 运行 GLFW 测试**

Run: `go test ./internal/driver/glfw -v`
Expected: PASS；测试不得默认打开需要人工关闭的永久窗口。

- [ ] **Step 5: 提交（仅已明确授权时）**

```bash
git add go.mod go.sum internal/driver/glfw
git commit -m "feat: implement GLFW window and input backend"
```

### Task 4: OpenGL 图元、视口与裁剪

**Files:**
- Create: `internal/render/render.go`
- Create: `internal/render/gl/renderer.go`
- Create: `internal/render/gl/projection.go`
- Create: `internal/render/gl/shaders.go`
- Modify: `go.mod`
- Test: `internal/render/gl/projection_test.go`

**Interfaces:**
- Consumes: Task 1 的 `driver.Window`、`driver.Size`、`driver.Point`；Task 3 窗口的 `MakeCurrent`、`FramebufferSize`、`Present`。
- Produces: `render.Target`、`render.Color`、`render.Rect` 和 OpenGL `Renderer`。

- [ ] **Step 1: 写失败的坐标转换测试**

```go
func TestProjectLogicalCorners(t *testing.T) {
    // 800x600：左上 -> (-1, 1)，右下 -> (1, -1)。
}
func TestClipPixelsHighDPI(t *testing.T) {
    // 逻辑 400x300、framebuffer 800x600，Rect{10, 20, 30, 40}
    // 必须得到物理像素 x=20、y=480、宽=60、高=80（OpenGL 使用底部原点）。
}
```

补充零尺寸 framebuffer 测试，要求不产生除零或 NaN。

- [ ] **Step 2: 运行测试并确认失败**

Run: `go test ./internal/render/gl -run 'TestProject|TestClip' -v`
Expected: FAIL，坐标函数尚未实现。

- [ ] **Step 3: 实现渲染抽象和 OpenGL Renderer**

```go
type Color struct { R, G, B, A float32 }
type Rect struct { X, Y, Width, Height float64 }
type Target interface {
    driver.Window
    MakeCurrent() error
    FramebufferSize() driver.Size
    Present()
}

func New(target render.Target) (*Renderer, error)
func (r *Renderer) Begin(clear render.Color) error
func (r *Renderer) DrawRect(rect render.Rect, color render.Color)
func (r *Renderer) SetClip(rect render.Rect)
func (r *Renderer) ClearClip()
func (r *Renderer) End() error
func (r *Renderer) Close()
```

添加固定 OpenGL 依赖。`Begin` 每次读取逻辑尺寸和 framebuffer 尺寸，设置物理 viewport 和逻辑坐标投影；`End` 才调用 `Present`。实现纯色顶点/片段 shader、矩形顶点、alpha blending 和 scissor。零尺寸 framebuffer 时跳过绘制和交换，不返回 NaN 错误。

- [ ] **Step 4: 运行纯逻辑测试并编译渲染器**

Run:

```bash
go test ./internal/render/gl -v
go test ./internal/render/...
```

Expected: PASS；真实 GPU 输出由 Task 6 验证，不用像素截图替代坐标测试。

- [ ] **Step 5: 提交（仅已明确授权时）**

```bash
git add go.mod go.sum internal/render
git commit -m "feat: add OpenGL primitive renderer"
```

### Task 5: 基础字体光栅化与文字绘制

**Files:**
- Create: `internal/text/face.go`
- Create: `internal/render/gl/text.go`
- Modify: `go.mod`
- Test: `internal/text/face_test.go`

**Interfaces:**
- Consumes: Task 4 的 `Renderer`、`render.Color`、`driver.Point`。
- Produces: `text.NewFace`、`Face.Rasterize` 和 `Renderer.DrawText`。

- [ ] **Step 1: 写失败的字体测试**

使用 `goregular.TTF` 和 16px 字号：

```go
func TestRasterizeText(t *testing.T)       // "Wind" 产生非零尺寸和非空 Alpha 位图。
func TestRasterizeEmpty(t *testing.T)      // 空字符串产生零尺寸，不报错。
func TestRasterizeInvalidUTF8(t *testing.T) // 0xff 不 panic、不报错，按 RuneError 处理。
func TestNewFaceRejectsInvalidFont(t *testing.T)
```

- [ ] **Step 2: 运行测试并确认失败**

Run: `go test ./internal/text -v`
Expected: FAIL，`NewFace` 尚未实现。

- [ ] **Step 3: 实现字体和 OpenGL 文字路径**

Run: `go get golang.org/x/image@v0.47.0`

```go
func NewFace(data []byte, size float64) (*Face, error)
func (f *Face) Rasterize(value string) (Bitmap, error)
func (r *Renderer) DrawText(value string, at driver.Point, color render.Color, face *text.Face) error
```

`Bitmap` 至少包含宽度、高度、Stride、Alpha 像素和逐字形定位信息。使用 `opentype.Parse`/`opentype.NewFace` 与字体 Drawer 光栅化；迭代字符串前使用 `utf8.DecodeRuneInString` 的正常错误替换语义。空文本直接返回空位图。OpenGL 侧上传 Alpha 纹理并使用文字颜色调制，绘制完成后按纹理生命周期释放或缓存。

- [ ] **Step 4: 运行文本与渲染测试**

Run: `go test ./internal/text ./internal/render/... -v`
Expected: PASS，无非法 UTF-8 panic。

- [ ] **Step 5: 提交（仅已明确授权时）**

```bash
git add go.mod go.sum internal/text internal/render/gl/text.go
git commit -m "feat: render basic font text"
```

### Task 6: 双窗口端到端烟雾程序

**Files:**
- Create: `cmd/wind-stage0/main.go`
- Test: `cmd/wind-stage0/main_test.go`（仅测试场景配置，不打开真实窗口）
- Modify: `go.mod`

**Interfaces:**
- Consumes: 前五个任务的 App、GLFW、OpenGL 和文本接口。
- Produces: 可人工运行的双窗口演示，以及可自动退出的 `--smoke` 验证入口。

- [ ] **Step 1: 写失败的场景测试并确认入口缺失**

`TestSmokePlanClosesBothWindows` 使用实现下述接口的假 App，调用 `closeAllAndQuit` 后断言两个窗口都先关闭、`Quit` 最后只调用一次：

```go
type demoApp interface {
    Windows() []driver.Window
    CloseWindow(driver.Window) error
    Quit()
}
func closeAllAndQuit(a demoApp) error
```

随后运行：

Run: `go run ./cmd/wind-stage0 --smoke`
Expected: FAIL，入口和 `closeAllAndQuit` 尚不存在。

- [ ] **Step 2: 实现双窗口演示**

`main` 创建 `app.New(glfw.New())`，预投递初始化函数并调用一次 `Run()`。初始化函数创建 640x400 和 400x240 两个窗口，为每个窗口建立独立 Renderer，绘制不同背景、矩形和 `"Wind stage 0"` 文本。

事件要求：

- 关闭请求只调用 `CloseWindow` 关闭对应窗口。
- 仅当应用开发者定义的演示策略发现 `Windows()` 为空时调用 `Quit()`。
- Resize/FramebufferResize/Paint 后按事件重绘；没有变化时不持续重绘。
- Rune 事件写入当前演示文本，Escape 只关闭对应窗口。
- 从工作 goroutine 通过 `Post` 切换一个矩形颜色，证明跨 goroutine 更新路径。

`--smoke` 使用 `time.AfterFunc` 投递绘制验证，然后调用 `closeAllAndQuit`；它逐个关闭 `Windows()` 返回的副本，全部关闭成功后才调用 `Quit()`。所有窗口操作仍只在 UI 线程执行。非 smoke 模式用于人工检查两个窗口、输入和高分辨率表现。

- [ ] **Step 3: 运行完整标准验证**

Run:

```bash
go mod tidy
go test ./...
go test -race ./internal/app ./internal/driver/fake
go vet ./...
go build ./cmd/wind-stage0
go run ./cmd/wind-stage0 --smoke
```

Expected: 全部退出码为 0；smoke 在真实 `DISPLAY` 上创建两个窗口后自行退出，无 panic、cgo 错误或卡死。`go build` 的默认输出文件在验证后删除，不保留任务不需要的二进制。

- [ ] **Step 4: 人工验证真实桌面场景**

Run: `go run ./cmd/wind-stage0`

Expected: 两个窗口可分别获得焦点、接收鼠标与文本输入；关闭其中一个不会退出另一个；关闭全部后应用有序退出。若当前会话无法人工操作，明确报告此项未验证，不用 smoke 冒充人工验收。

- [ ] **Step 5: 提交（仅已明确授权时）**

```bash
git add go.mod go.sum cmd/wind-stage0
git commit -m "feat: add native multi-window stage zero demo"
```
