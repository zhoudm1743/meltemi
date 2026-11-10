# Stage 1E Linux X11/XIM Basic IME Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在现有 Go/GLFW/OpenGL 文本组件上实现 Linux X11/XIM 基础 IME，支持预编辑显示、提交去重、候选位置、取消和资源释放。

**Architecture:** 先以项目内 `internal/xim` 封装 Fcitx D-Bus 输入上下文、预编辑信号和生命周期，再在现有 GLFW 后端接入 D-Bus 按键处理。文本提交只经一个路径进入组件，预编辑独立保存，不改变绑定值；候选位置使用客户端坐标。

**Tech Stack:** Go 1.26、`github.com/godbus/dbus/v5@v5.1.0`、既有 GLFW/OpenGL/text/ui；不 fork、不 vendor、不使用 replace 修改 GLFW，不新增大型依赖。

**Spec:** `docs/superpowers/specs/2026-11-04-text-input-css-ime-expanded-design.md` 的 1E 节及补充裁决。

## Global Constraints

- 仅 Linux X11/Fcitx D-Bus，使用项目内 `internal/xim` 客户端；当前验证环境为 Fcitx 5，不承诺 Wayland/Windows/macOS 或其他输入法框架。
- 不 fork、不 vendor、不使用 replace 修改 `go-gl/glfw`，不引入 WebView。
- 预编辑与已提交文本分开保存，普通 Rune 与 IME 提交不得重复。
- 组合开始、更新、提交、取消和销毁必须清理组合状态及 D-Bus 输入上下文和连接。
- 候选位置使用当前 caret 的客户端坐标，窗口移动或滚动时同步。
- 所有 D-Bus 调用、回调和 UI 状态只在 UI 线程；D-Bus 输入上下文随应用创建和销毁，连接随应用关闭。
- 真实 Fcitx 测试仅 opt-in，不能把普通 Rune 输入冒充为 IME 验证；无法可靠实现预编辑时必须报告阻塞。
- 当前非 Git 仓库，不自动初始化或提交；不使用 sudo。

## Review Focus

- XIM 与 GLFW 同时存在：预编辑能更新，但提交不得重复；由 Task 1 的 `TestXIMPreeditAndCommitOnce` 固定。
- 多 rune 提交与取消：每次提交只插入一次，取消后无残留组合；由 Task 2 的 `TestCompositionCommitAndCancel` 固定。
- 焦点切换、禁用、隐藏和销毁：必须取消组合并释放 XIC/XIM；由 Task 2/3 的 `TestCompositionLifecycleCleanup` 固定。
- 候选位置：caret 移动、水平/垂直滚动后 spot 使用最新客户端坐标；由 Task 3 的 `TestIMESpotFollowsCaret` 固定。
- 真实 Fcitx 预编辑和提交：显示预编辑、提交中文一次且不重复；由 Task 4 的真实 X11/Fcitx 验证固定。

---

### Task 1: Fcitx D-Bus 客户端与预编辑技术验证

**Files:**
- Create: `internal/xim/client.go`
- Create: `internal/xim/client_dbus.go`
- Create: `internal/xim/composition.go`
- Modify: `go.mod`
- Test: `internal/xim/client_test.go`
- Test: `internal/xim/composition_test.go`

**Interfaces:**
- Consumes: Fcitx `org.fcitx.Fcitx.InputMethod1/InputContext1` D-Bus 接口和当前 session bus。
- Produces: `Client`、`Preedit`、`ProcessKeyEvent`、`SetFocus`、`SetSpot`、`Close`，供 Task 2 后端使用。

- [ ] **Step 1: 实现纯 Go 组合状态和 D-Bus 客户端骨架**

`Preedit` 包含 Text、Caret、Active。纯 Go 状态测试覆盖开始、更新、提交、取消、多 rune 和索引钳制。客户端持有 D-Bus 连接和输入上下文，Close 先 DestroyIC 再关闭连接。

- [ ] **Step 2: 实现输入上下文创建、信号和焦点/Spot**

```go
func NewClient(ctx context.Context, program, display string) (*Client, error)
func (c *Client) SetFocus(focused bool) error
func (c *Client) SetSpot(x, y, width, height int) error
func (c *Client) Preedit() Preedit
func (c *Client) Close() error
```

使用 `CreateInputContext` 创建持久 D-Bus 输入上下文，监听 `UpdateFormattedPreedit` 获取文本和光标，`CommitString` 作为唯一提交信号，`FocusIn/FocusOut` 管理焦点，`SetCursorRect` 使用客户端坐标。关闭时先取消组合再 DestroyIC、关闭连接。

- [ ] **Step 3: 实现按键处理与提交去重**

```go
func (c *Client) ProcessKeyEvent(keyval, keycode, state uint32, isRelease bool, time uint32) (bool, error)
```

按键通过 D-Bus `ProcessKeyEvent` 发送，preedit 更新只来自 `UpdateFormattedPreedit`，提交文本只来自一次 `CommitString` 并转为一次 `driver.EventRune`。不得同时调用 Xutf8LookupString、第二个 D-Bus ProcessKeyEvent 或 GLFW char callback 生成重复文本。

- [ ] **Step 4: 在真实 X11/Fcitx 环境验证**

使用持久 D-Bus 连接验证：预编辑更新、SetCursorRect、一次中文 CommitString 只到达一次、FocusOut/DestroyIC 无残留。若服务或输入法不可用，明确 Skip 原因，不伪造结果。

- [ ] **Step 5: 运行纯 Go 测试和真实技术验证**

Run:

```bash
go get github.com/godbus/dbus/v5@v5.1.0
go test ./internal/xim -v
WIND_GLFW_DESKTOP_TESTS=1 go test ./internal/xim -run TestXIMPreeditAndCommitOnce -v
```

Expected: 纯 Go 测试 PASS，真实验证预编辑可见且提交一次；无法可靠实现时停止后续任务并报告。

- [ ] **Step 6: 提交（仅已明确授权时）**

```bash
git add go.mod go.sum internal/xim
git commit -m "feat: add Fcitx D-Bus input context bridge"
```

### Task 2: GLFW D-Bus 按键处理与组合生命周期

**Files:**
- Create: `internal/driver/glfw/xim.go`
- Modify: `internal/driver/glfw/backend.go`
- Modify: `internal/driver/glfw/window.go`
- Modify: `internal/driver/glfw/events.go`
- Modify: `internal/driver/types.go`
- Modify: `internal/driver/driver.go`
- Test: `internal/driver/glfw/xim_test.go`

**Interfaces:**
- Consumes: Task 1 的 Fcitx D-Bus Client 和 GLFW 原生 Display/Window。
- Produces: `CompositionWindow`、组合事件和 D-Bus 按键处理路径。

- [ ] **Step 1: 写失败的按键处理和生命周期测试**

```go
func TestXIMFilteredKeyDoesNotReachGLFWTwice(t *testing.T)
func TestCompositionCommitAndCancel(t *testing.T)
func TestCompositionLifecycleCleanup(t *testing.T)
```

使用 fake Fcitx client 验证：D-Bus 消费的按键不进入 GLFW，CommitString 只发一次 EventRune；未消费按键仍走原路径。焦点变化、禁用、Hide、Destroy 和 Terminate 都取消组合并 Close client。

- [ ] **Step 2: 运行测试并确认失败**

Run: `go test ./internal/driver/glfw -run 'TestXIM|TestComposition' -v`
Expected: FAIL，D-Bus 按键处理和组合接口尚未实现。

- [ ] **Step 3: 实现 D-Bus 按键处理与组合事件**

Backend.Init 在 `WIND_IME=1` 时创建 Task 1 的持久 Fcitx client。按键先通过 `ProcessKeyEvent(keyval,keycode,state,isRelease,time)` 发送，D-Bus 返回 true 时不向组件重复投递 EventKey；返回 false 时沿用原 EventKey。CommitString 只通过一次 EventRune 进入组件，不再调用 GLFW char callback 或第二次 ProcessKeyEvent。

`driver.Event` 增加 `ScanCode` 和 `KeySym` 字段，GLFW key 回调保存原始 scancode 和 KeySym，供 D-Bus 使用。新增事件：

```go
EventCompositionStart
EventCompositionUpdate
EventCompositionEnd
```

`CompositionWindow` 提供 `Preedit() (text string, caret int, active bool)` 和 `SetSpot(x, y int)`，供 Task 3 使用。提交去重依赖 Task 1 的单一路径。

- [ ] **Step 4: 实现焦点和资源生命周期**

按窗口创建/关闭 Fcitx client。焦点丢失、组件禁用、窗口隐藏/销毁和 Backend.Terminate 必须取消组合并 DestroyIC/关闭连接；Destroy 仍先清回调再延迟原生销毁，不得在 GLFW 回调栈中销毁窗口。

- [ ] **Step 5: 运行后端和全量测试**

Run: `go test ./internal/driver/glfw ./internal/xim -v`
Expected: PASS，无重复提交、无组合或 D-Bus 资源残留。

- [ ] **Step 6: 提交（仅已明确授权时）**

```bash
git add internal/xim internal/driver
git commit -m "feat: integrate Fcitx D-Bus into GLFW event loop"
```

### Task 3: Input/TextArea 预编辑显示与候选位置

**Files:**
- Create: `ui/composition.go`
- Modify: `ui/input.go`
- Modify: `ui/input_draw.go`
- Modify: `ui/textarea.go`
- Modify: `ui/textarea_draw.go`
- Modify: `ui/window.go`
- Modify: `ui/app.go`
- Modify: `ui/event.go`
- Test: `ui/composition_test.go`

**Interfaces:**
- Consumes: Task 2 的 CompositionWindow、组合事件和现有文本/滚动模型。
- Produces: Input/TextArea 的预编辑显示、焦点取消和 caret spot 更新。

- [ ] **Step 1: 写失败的组件组合测试**

```go
func TestInputPreeditDoesNotChangeBinding(t *testing.T)
func TestCompositionEndInsertsOnce(t *testing.T)
func TestIMESpotFollowsCaret(t *testing.T)
func TestInputDisableCancelsComposition(t *testing.T)
```

预编辑只改变显示，不改 Value/绑定/历史；提交后文本只进入一次。水平/垂直滚动和 caret 移动后 SetSpot 参数为最新客户端坐标。

- [ ] **Step 2: 运行测试并确认失败**

Run: `go test ./ui -run 'TestInputPreedit|TestComposition|TestIMESpot' -v`
Expected: FAIL，组件组合显示尚未实现。

- [ ] **Step 3: 实现预编辑显示和焦点取消**

Input/TextArea 增加内部组合状态，使用不同颜色或下划线显示预编辑，不修改 textBuffer/绑定/历史。组合结束只通过唯一提交路径插入。焦点切换、SetEnabled(false)、Hide/Close 清空组合显示并调用后端取消。

新增 `App.EnableIME() error`：仅在 `Run()` 前允许调用，设置 `WIND_IME=1` 和 `XMODIFIERS=@im=none`，让 Backend 使用 D-Bus 组合输入而不再经过 GLFW XIM。默认未启用 IME 的应用保持原有输入路径；Run 开始后调用返回错误。

- [ ] **Step 4: 实现 caret spot 客户端坐标**

Input 使用 `content.X + caretOffset - scrollX` 和文本行底；TextArea 使用对应行列位置。Window 在 caret/滚动变化时调用 CompositionWindow.SetSpot，使用客户端坐标，不叠加窗口 Position。

- [ ] **Step 5: 运行组件和全量测试**

Run:

```bash
go test ./ui -v
go test -race ./ui ./internal/app
go test ./...
```

Expected: PASS，预编辑不重复、不污染绑定，spot 与 caret 一致。

- [ ] **Step 6: 提交（仅已明确授权时）**

```bash
git add ui internal/driver
git commit -m "feat: render IME preedit and caret spot"
```

### Task 4: Fcitx 真实预编辑与提交演示

**Files:**
- Create: `cmd/wind-ime/main.go`

**Interfaces:**
- Consumes: 前三个任务的公开 API 和真实 X11/Fcitx。
- Produces: 可运行的 `wind-ime` 和 opt-in 真实验证。

- [ ] **Step 1: 实现 IME 演示窗口**

使用公开 Input、TextArea 和 Label，注册中文字体，显示预编辑和提交结果。不得导入内部 XIM/driver 类型或修改用户输入法配置。

- [ ] **Step 2: 实现真实 Fcitx 验证**

在 `WIND_GLFW_DESKTOP_TESTS=1` 的真实 X11 会话中，使用当前输入法执行：组合开始 → 预编辑更新 → 候选位置变化 → 选择候选提交 → 再输入一次 → 取消。验证中文只提交一次、无重复、无组合残留，隐藏和销毁无残留 XIM 资源。

- [ ] **Step 3: 运行完整标准验证**

Run:

```bash
go test ./...
go test -race ./ui ./internal/app ./internal/driver/glfw ./internal/xim
go vet ./...
go build ./cmd/wind-ime
WIND_GLFW_DESKTOP_TESTS=1 go run ./cmd/wind-ime --smoke
```

Expected: 全部退出码 0；真实预编辑显示、提交一次、取消和释放成功。删除构建产物。

- [ ] **Step 4: 验证真实桌面观感**

Run: `go run ./cmd/wind-ime`

Expected: Fcitx 预编辑和候选可见，候选位置随 caret 更新，中文提交不重复，取消和关闭正常。无法验证其他输入法、Wayland、真人手感或高 DPI 必须披露。

- [ ] **Step 5: 提交（仅已明确授权时）**

```bash
git add cmd/wind-ime
git commit -m "feat: add XIM IME demo"
```
