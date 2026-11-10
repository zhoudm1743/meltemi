# Stage 1 Menus and Window Capabilities Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现统一菜单模型、Linux 自绘菜单栏、Overlay/Popup、实际菜单快捷键，以及窗口配置、焦点/尺寸通知和可配置的最后可见窗口策略。

**Architecture:** 菜单是纯 Go 描述模型，Linux 由组件树和窗口级 overlay 绘制，不依赖 GTK。Overlay 由 Window/eventRouter 持有，在内容之后绘制并优先接收事件；快捷键只在未被聚焦组件消费时匹配。窗口后端通过扩展内部接口支持标题、最小尺寸、图标和焦点事件，公共 API 不暴露后端类型。

**Tech Stack:** Go 1.26、既有 GLFW/OpenGL、内部 driver/app/style；不新增第三方依赖。

**Spec:** `doc/总体设计.md` 第 2、3.2、5.1、5.2、7.4、8、9.1、10、13、14、15 节。

## Global Constraints

- Linux 菜单为窗口内自绘；不声称所有平台使用系统菜单。
- 不使用 Fyne、WebView、HTML 或 JavaScript，不新增依赖，不公开任意 Canvas。
- Esc 先关闭最上层 overlay；无 overlay 时只发送窗口取消事件，不自动关闭窗口。
- 快捷键只在按键未被组件消费时触发，disabled 项不得触发，checked 状态与回调保持同步。
- 多窗口的 overlay、焦点、菜单和关闭策略完全隔离；UI 回调只在 UI 线程执行。
- 当前不是 Git 仓库，不初始化、不提交、不使用 sudo。

## Review Focus

- 嵌套 submenu 与外部点击：只关闭应关闭的层，不能让一次事件穿透触发底层按钮；由 Task 3 的 `TestOverlayNestedDismissAndPointerCapture` 固定。
- 正在编辑的 Input/TextArea：其消费的快捷键不得同时触发菜单；由 Task 2 的 `TestMenuShortcutAfterFocusedComponent` 固定。
- IME 组合期间 Esc/快捷键：保持现有组合生命周期，不能误触发菜单；由 Task 2 的 `TestMenuShortcutDuringComposition` 固定。
- disabled 或隐藏窗口的菜单/回调：不得触发动作或向已销毁后端投递；由 Task 4 的 `TestWindowMenuLifecycleIsolation` 固定。
- CloseHide 与最后可见窗口：隐藏最后一个可见窗口应按配置退出、保持或询问回调；OnClose veto 不触发；由 Task 1 的 `TestLastVisibleWindowPolicies` 固定。

---

### Task 1: 窗口配置、事件与最后可见窗口策略

**Files:**
- Modify: `ui/window.go`, `ui/app.go`, `internal/driver/types.go`, `internal/driver/driver.go`, `internal/driver/glfw/window.go`, `internal/driver/glfw/events.go`, `internal/driver/fake/fake.go`
- Test: `ui/window_test.go`, `ui/app_test.go`, `internal/driver/glfw/window_test.go`, `internal/driver/glfw/events_test.go`

**Interfaces:**
- Window 新增 `Title(string) *Window`、`SetTitle(string)`、`MinSize(width,height int) *Window`、`Icon(image.Image) *Window`、`OnFocus(func(bool))`、`OnResize(func(width,height int))`、`OnCancel(func())`；`MenuBar(...*Menu)` 由 Task 4 在菜单模型和 Overlay 就绪后添加。
- App 新增 `SetLastWindowCallback(func() LastWindowAction)`；回调优先于静态 action。策略在最后一个可见窗口销毁或隐藏后执行，veto 不执行；`LastWindowQuit/KeepRunning` 保持原值。
- driver 配置携带最小尺寸和图标；窗口接口可用小型可选扩展避免无关测试 double 被迫实现全部方法。新增 focus 事件包含 bool。

- [ ] **Step 1: 写失败测试**：`TestWindowConfigurationAndNativeEvents`、`TestWindowFocusResizeCancelCallbacks`、`TestLastVisibleWindowPolicies`、`TestWindowIconAndMinSize`，覆盖 Show 前配置、Show 后 SetTitle、隐藏后再 Show、销毁后无回调。
- [ ] **Step 2: 运行 `go test ./ui ./internal/driver/... -run 'Test(Window|LastVisible)' -v`，确认失败。**
- [ ] **Step 3: 实现 GLFW 标题、大小限制、图标与 focus/resize/cancel 转换。** 图标使用标准库 image.Image，在 native 创建时复制所需像素，所有权清晰。
- [ ] **Step 4: 在 App/Window 中跟踪真实 visible 状态并实现 callback 策略；更新原“Hide 永不触发”注释和测试以符合总体设计。** MenuBar 字段本任务只保存，由后续任务消费。
- [ ] **Step 5: 运行 `go test ./ui ./internal/driver/...`，确认全部通过。**

### Task 2: 菜单描述模型与快捷键分发

**Files:**
- Create: `ui/menu.go`, `ui/menu_test.go`
- Modify: `internal/driver/types.go`, `internal/driver/glfw/events.go`, `ui/event.go`

**Interfaces:**
- Produces: `Menu(title string, items ...*MenuItem) *Menu`、`MenuItem(text string) *MenuItem`、`MenuSeparator() *MenuItem`。
- MenuItem 提供 `OnClick(func())`、`SetEnabled(bool)`、`Enabled() bool`、`SetChecked(bool)`、`Checked() bool`、`Shortcut(mods Modifiers, key rune) *MenuItem`、`Submenu(...*MenuItem) *MenuItem`。
- 公共 `Modifiers` 为 bitmask，常量为 `ModShift/ModCtrl/ModAlt/ModSuper`；快捷键 rune 只接受 ASCII 字母和数字，字母大小写归一，Shift 仍按 modifiers 精确匹配。

- [ ] **Step 1: 写失败测试**：`TestMenuModelAndShortcutNormalization`、`TestMenuShortcutAfterFocusedComponent`、`TestMenuDisabledAndCheckedItems`、`TestMenuShortcutDuringComposition`。
- [ ] **Step 2: 运行 `go test ./ui ./internal/driver/glfw -run 'TestMenu' -v`，确认失败。**
- [ ] **Step 3: 实现菜单树、输入校验、状态与回调。** 触发普通可勾选菜单项时切换 checked；separator/submenu 不直接触发点击。
- [ ] **Step 4: 将 GLFW ASCII 键和修饰键转换为后端无关 shortcut 数据，eventRouter 在 focused component 未消费后做深度优先匹配。** IME 已消费的按键不能再次进入菜单。
- [ ] **Step 5: 运行 `go test ./ui ./internal/driver/...`，确认全部通过。**

### Task 3: Overlay/Popup 层

**Files:**
- Create: `ui/overlay.go`, `ui/overlay_test.go`
- Modify: `ui/window.go`, `ui/event.go`, `ui/component.go`, `ui/app.go`

**Interfaces:**
- 内部 `overlay` 持有独立 component 子树、bounds、dismiss 回调和可选父子关系；Window 提供内部 `pushOverlay`、`popOverlay`、`clearOverlays`。
- overlay 在 root 后绘制，具有裁剪与最高事件优先级；最上层捕获 pointer，外部 press 关闭相应弹出链且事件不穿透，Esc 只关闭最上层。
- Content 替换、Close、Hide、destroy、finalize 必须取消 overlay 的焦点、press、composition 和 clip。

- [ ] **Step 1: 写失败测试**：`TestOverlayDrawOrderAndClip`、`TestOverlayNestedDismissAndPointerCapture`、`TestOverlayEscapeAndFocus`、`TestOverlayWindowLifecycleCleanup`。
- [ ] **Step 2: 运行 `go test ./ui -run 'TestOverlay' -v`，确认失败。**
- [ ] **Step 3: 实现 overlay 栈及独立样式遍历、布局、绘制和事件路由。** 点击 submenu 所属父层不能误关整个菜单链；新焦点转移不得泄漏旧 composition。
- [ ] **Step 4: 实现外部点击、Esc、层级关闭和资源清理；无 overlay 的 Esc 传给 Window.OnCancel。**
- [ ] **Step 5: 运行 `go test ./ui`，确认全部通过。**

### Task 4: Linux 自绘 MenuBar 与菜单弹出层

**Files:**
- Create: `ui/menubar.go`, `ui/menubar_test.go`, `cmd/wind-menu/main.go`, `cmd/wind-menu/main_test.go`
- Modify: `ui/window.go`, `ui/style.go`, `ui/column.go`, `internal/style/parse.go`, `internal/style/parse_test.go`

**Interfaces:**
- `Window.MenuBar(...*Menu)` 在内容顶部安装自绘菜单栏，内容尺寸扣除菜单栏高度；运行中替换菜单栏会清理旧 overlay/focus。
- 新增内部组件类型 `MenuBar/MenuItem` 的样式元数据，复用颜色、字体、padding、border、radius、opacity 与状态选择器；菜单 CSS 不承诺系统托盘菜单外观。

- [ ] **Step 1: 写失败测试**：`TestMenuBarLayoutAndPopup`、`TestMenuBarSubmenuAndOutsideDismiss`、`TestMenuBarKeyboardAndShortcut`、`TestWindowMenuLifecycleIsolation`。
- [ ] **Step 2: 运行 `go test ./ui ./internal/style -run 'TestMenu' -v`，确认失败。**
- [ ] **Step 3: 实现菜单栏和弹出菜单组件、submenu 定位、视口边界钳制、hover/pressed/disabled/checked 绘制及回调。** 菜单位置使用窗口逻辑坐标，不依赖操作系统全局菜单位置。
- [ ] **Step 4: 将 Window 菜单模型与快捷键路由、Overlay 栈连接，覆盖替换 Content/MenuBar 与关闭窗口的清理。**
- [ ] **Step 5: 添加真实多窗口菜单演示，运行 `go test ./...`、`go vet ./...` 和修改文件的 `gofmt` 检查；真实 smoke 单独记录结果。**
