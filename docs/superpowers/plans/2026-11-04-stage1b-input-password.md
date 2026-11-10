# Stage 1B Input and Password Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在公开 `wind/ui` 包中实现 Input/Password 单行组件，支持 UTF-8 编辑、光标、选择、系统剪贴板、指针绑定、密码掩码和真实登录演示。

**Architecture:** 先用 rune 索引的纯 Go 文本模型实现编辑与选择，再通过内部 driver.Clipboard 接入 GLFW 系统剪贴板。InputWidget 实现既有 component/focusable/pointerTarget 契约，窗口注入字体和剪贴板，painter 增加与既有 GL 一致的裁剪能力。

**Tech Stack:** Go 1.26、既有公开 ui、内部 driver/GLFW/OpenGL/text；不新增第三方依赖。

**Spec:** `doc/总体设计.md` 第 5.3、5.4、6、10、11.2、13、14、15 节。

## Global Constraints

- 不使用 Fyne、WebView、HTML 或 JavaScript，不新增依赖。
- 本阶段只实现单行 Input/Password，不实现多行、撤销/重做、IME、CSS、菜单或托盘。
- 公共 API 不暴露 GLFW/OpenGL/内部 driver 类型；组件与 UI 状态只在 UI 线程。
- 文本编辑使用 rune 索引，不得按字节拆分中文、emoji 或非法 UTF-8 替换字符。
- `SetValue` 同时更新绑定值和显示；直接修改绑定指针不会自动通知，工作 goroutine 必须经 App.Post。
- 剪贴板接口保持内部可选能力，缺失时复制/剪切/粘贴安全 no-op，不 panic。
- 真实桌面剪贴板测试必须保存并恢复旧值；干扰型测试沿用 `WIND_GLFW_DESKTOP_TESTS=1` opt-in。
- 当前非 Git 仓库，不自动初始化或提交；不使用 sudo。

## Review Focus

- 中文、emoji 和非法 UTF-8：左右移动、删除、选择不得拆分 rune；由 Task 1 的 `TestTextBufferUnicodeEditing` 固定。
- 绑定值直接修改与 SetValue 的区别：只有 SetValue 同步组件和指针并触发后续 Post 重绘；由 Task 3 的 `TestInputSetValueAndBinding` 固定。
- Password 的显示与真实值：掩码不能改变 Value、选择或剪贴板内容；由 Task 3 的 `TestPasswordMaskAndClipboard` 固定。
- 系统剪贴板不可用或返回错误：编辑和绘制仍正常，复制/粘贴安全 no-op；由 Task 2/3 的 `TestClipboardUnavailable` 固定。
- 拖动选择后离开窗口并释放：不得误改焦点或插入文本，选择状态必须一致；由 Task 3 的 `TestInputDragSelectionAndOutsideRelease` 固定。

---

### Task 1: rune 文本编辑与选择模型

**Files:**
- Create: `ui/textedit.go`
- Test: `ui/textedit_test.go`

**Interfaces:**
- Consumes: 无既有后端，使用包内 `textClipboard` 接口。
- Produces: `textBuffer`、`textClipboard`，供 Task 3 的 InputWidget 使用。

- [ ] **Step 1: 写失败的文本模型测试**

覆盖：

```go
func TestTextBufferInsertBackspaceDelete(t *testing.T)
func TestTextBufferUnicodeEditing(t *testing.T)      // 中文、emoji、非法 UTF-8 替换。
func TestTextBufferCursorHomeEndAndShift(t *testing.T)
func TestTextBufferSelectionCopyCutPaste(t *testing.T)
func TestTextBufferMouseAnchorAndExtend(t *testing.T)
```

示例：`"a中🙂b"` 的 rune 长度为 4；左右移动、Home/End、Shift 扩选、Backspace/Delete 均按 rune。`SetValue` 将非法 UTF-8 归一化为 U+FFFD，而不是产生分裂字节。

- [ ] **Step 2: 运行测试并确认失败**

Run: `go test ./ui -run TestTextBuffer -v`
Expected: FAIL，textBuffer 尚未实现。

- [ ] **Step 3: 实现 textBuffer 和剪贴板抽象**

```go
type textClipboard interface {
    Text() (string, error)
    SetText(string) error
}

type textBuffer struct { /* runes、cursor、selection anchor */ }
func newTextBuffer(value string) *textBuffer
func (b *textBuffer) SetValue(value string)
func (b *textBuffer) Value() string
func (b *textBuffer) Cursor() int
func (b *textBuffer) Selection() (start, end int, ok bool)
func (b *textBuffer) Insert(r rune)
func (b *textBuffer) Backspace()
func (b *textBuffer) Delete()
func (b *textBuffer) Move(delta int, extend bool)
func (b *textBuffer) Home(extend bool)
func (b *textBuffer) End(extend bool)
func (b *textBuffer) SelectAll()
func (b *textBuffer) StartSelection(index int)
func (b *textBuffer) ExtendSelection(index int)
func (b *textBuffer) Copy(cb textClipboard) error
func (b *textBuffer) Cut(cb textClipboard) error
func (b *textBuffer) Paste(cb textClipboard) error
```

索引始终位于 rune 边界并钳制到 `[0,len]`。非 Shift 移动清除选择，Shift 移动保留/扩展 anchor。无选择时 Cut/Copy 不改文本；Paste 替换当前选择。nil clipboard 或错误不修改文本。

- [ ] **Step 4: 运行文本模型测试**

Run: `go test ./ui -run TestTextBuffer -v`
Expected: PASS。

- [ ] **Step 5: 提交（仅已明确授权时）**

```bash
git add ui/textedit.go ui/textedit_test.go
git commit -m "feat: add unicode text editing model"
```

### Task 2: 内部剪贴板能力与控制键

**Files:**
- Modify: `internal/driver/types.go`
- Modify: `internal/driver/driver.go`
- Modify: `internal/driver/glfw/window.go`
- Modify: `internal/driver/glfw/events.go`
- Modify: `internal/driver/glfw/events_test.go`
- Test: `internal/driver/glfw/clipboard_test.go`

**Interfaces:**
- Consumes: Task 1 的 textClipboard 语义；既有 GLFW Window 剪贴板函数。
- Produces: 内部 `driver.Clipboard` 和 `KeyC`、`KeyV`、`KeyX`，供 Task 3 使用。

- [ ] **Step 1: 写失败的按键和剪贴板测试**

`TestMapClipboardKeys` 断言 C/V/X 映射。`TestClipboardRoundTrip` 仅在 `WIND_GLFW_DESKTOP_TESTS=1` 时运行：先保存系统旧文本，写入唯一测试文本并读回，cleanup 恢复旧文本。默认运行只验证接口和不干扰系统剪贴板的逻辑。

- [ ] **Step 2: 运行测试并确认失败**

Run: `go test ./internal/driver/glfw -run 'TestMapClipboardKeys|TestClipboardRoundTrip' -v`
Expected: FAIL，KeyC/V/X 或 Clipboard 尚未实现。

- [ ] **Step 3: 实现内部 Clipboard 和按键映射**

```go
type Clipboard interface {
    Text() (string, error)
    SetText(string) error
}
```

GLFW Window 实现 Text/SetText；销毁后返回 errDestroyed，SetText 不得触碰原生资源。按键 C/V/X 按既有 mapKey 风格增加，不改变其他键和 Rune 事件。

- [ ] **Step 4: 运行驱动和全量测试**

Run:

```bash
go test ./internal/driver/glfw -v
go test ./...
WIND_GLFW_DESKTOP_TESTS=1 go test ./internal/driver/glfw -run TestClipboardRoundTrip -v
```

Expected: 默认与 opt-in 均 PASS，真实测试后剪贴板恢复旧值。

- [ ] **Step 5: 提交（仅已明确授权时）**

```bash
git add internal/driver
git commit -m "feat: add native clipboard support"
```

### Task 3: Input/Password 组件、绘制与事件集成

**Files:**
- Create: `ui/input.go`
- Create: `ui/input_draw.go`
- Modify: `ui/component.go`
- Modify: `ui/event.go`
- Modify: `ui/window.go`
- Test: `ui/input_test.go`
- Test: `ui/input_draw_test.go`
- Test: `ui/event_test.go`

**Interfaces:**
- Consumes: Task 1 的 textBuffer、Task 2 的 driver.Clipboard、既有 painter/eventRouter/FrameWindow。
- Produces: `InputWidget`、`Input`、`Password`，以及 painter 裁剪和窗口剪贴板注入。

- [ ] **Step 1: 写失败的 Input 行为测试**

覆盖：

```go
func TestInputTypingCursorAndSelection(t *testing.T)
func TestInputSetValueAndBinding(t *testing.T)
func TestInputClipboardShortcuts(t *testing.T)
func TestInputDragSelectionAndOutsideRelease(t *testing.T)
func TestInputDisabledAndFocus(t *testing.T)
func TestPasswordMaskAndClipboard(t *testing.T)
func TestInputPlaceholderAndHorizontalScroll(t *testing.T)
func TestClipboardUnavailable(t *testing.T)
```

`Input(&account).Placeholder("账号")` 和 `Password(&password).Placeholder("密码")` 为公开形态。Input 自然尺寸默认 200x36，高度固定，支持 Column 水平拉伸。

- [ ] **Step 2: 运行测试并确认失败**

Run: `go test ./ui -run 'TestInput|TestPassword|TestClipboardUnavailable' -v`
Expected: FAIL，InputWidget 尚未实现。

- [ ] **Step 3: 实现 InputWidget、公开 API 和文本绘制**

```go
func Input(value *string) *InputWidget
func Password(value *string) *InputWidget
func (i *InputWidget) Placeholder(text string) *InputWidget
func (i *InputWidget) Value() string
func (i *InputWidget) SetValue(value string)
func (i *InputWidget) Enabled() bool
func (i *InputWidget) SetEnabled(enabled bool)
```

实现 component、focusableComponent、pointerTarget、faceReceiver。Value 始终为真实值，Password 仅显示与文本等 rune 数的 `*`。绘制包含边框、背景、placeholder、选择背景、1px 光标和焦点边框；长文本按 cursor 调整 scrollX，裁剪到内容矩形。

- [ ] **Step 4: 扩展 painter、窗口注入和事件捕获**

painter 增加与现有 GL 一致的 `SetClip(render.Rect)` / `ClearClip()`。Window 初始化时向组件树注入 Face 和可选 driver.Clipboard；缺失时传 nil。

鼠标按下定位 rune 光标并建立 anchor，拖动更新选择，释放结束；eventRouter 在手势期间把 move/release 投给原 pressed 组件，保证离窗拖动语义。Ctrl+A/C/V/X 分别全选/复制/剪切/粘贴，Shift+方向/Home/End 扩选。SetEnabled(false) 清焦点和手势，不能编辑。

- [ ] **Step 5: 运行 Input 和全量测试**

Run:

```bash
go test ./ui -v
go test -race ./ui ./internal/app
go test ./...
```

Expected: PASS，覆盖中文、选择、掩码、滚动、禁用和剪贴板 no-op。

- [ ] **Step 6: 提交（仅已明确授权时）**

```bash
git add ui internal/driver
git commit -m "feat: add single line input and password widgets"
```

### Task 4: 链式登录窗口真实演示

**Files:**
- Create: `cmd/wind-login/main.go`

**Interfaces:**
- Consumes: 前三个任务的公开 API。
- Produces: 可运行的 `wind-login` 和 `--smoke` 验证。

- [ ] **Step 1: 先运行缺失入口确认 RED**

Run: `go run ./cmd/wind-login --smoke`
Expected: FAIL，入口尚不存在。

- [ ] **Step 2: 实现登录窗口**

保持以下公开 API 形态，不导入内部包：

```go
account := ""
password := ""

app.Window("登录").
    Size(360, 240).
    Content(
        ui.Column(
            ui.Input(&account).Placeholder("账号"),
            ui.Password(&password).Placeholder("密码"),
            ui.Button("登录").OnClick(login),
        ).Gap(12).Padding(20),
    ).
    Show()
```

增加状态 Label 展示点击结果，不显示密码明文。`app.Run()` 返回实际初始化/关闭错误。

- [ ] **Step 3: 实现 smoke 并运行完整验证**

`--smoke` 通过 Post 设置账号密码、点击登录、断言绑定值和状态文本，再关闭窗口。真实桌面可另行用合成键盘输入验证 Rune、Tab、选择、剪贴板和掩码；不得读取或遗留用户剪贴板内容。

Run:

```bash
go test ./...
go test -race ./ui ./internal/app
go vet ./...
go build ./cmd/wind-login
go run ./cmd/wind-login --smoke
```

Expected: 全部退出码 0，真实登录窗口创建并释放，无残留进程。删除构建产物。

- [ ] **Step 4: 验证真实桌面交互**

Run: `go run ./cmd/wind-login`

Expected: 可输入中文和 ASCII，光标/选择/复制剪切粘贴可用，Password 只显示掩码，Tab 切换焦点，按钮可点击。无法真人或高 DPI 验证必须披露。

- [ ] **Step 5: 提交（仅已明确授权时）**

```bash
git add cmd/wind-login
git commit -m "feat: add chained login window demo"
```
