# Stage 1 Tray and Capability Query Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现应用桌面能力查询、Linux StatusNotifierItem 托盘、图标/提示文本、D-Bus 托盘菜单、窗口恢复和明确销毁。

**Architecture:** 公共 Tray 与 UI App 解耦，Linux 适配器通过 session D-Bus 导出 StatusNotifierItem 和 com.canonical.dbusmenu，不依赖 GTK/AppIndicator 动态库。D-Bus 回调只投递到 App.Post，托盘对象自身并发安全；无 watcher、注册失败或平台不支持时返回明确错误。

**Tech Stack:** Go 1.26、既有 `github.com/godbus/dbus/v5`、标准库 image；不新增第三方依赖。

**Spec:** `doc/总体设计.md` 第 2、3.2、5.1、7.4、8.2、8.3、9.2、12、13、14、15 节。

## Global Constraints

- Linux 优先使用 StatusNotifierItem；不承诺 GNOME/KDE 等所有桌面环境都能显示托盘。
- 不支持或注册失败必须返回错误/能力状态，不静默假装成功。
- 托盘菜单由系统绘制，不接受本库 CSS；复用统一 Menu/MenuItem 描述、checked/enabled 和 Go 回调。
- D-Bus 连接、信号订阅和对象导出必须可重复销毁；销毁后回调不再修改 UI。
- 所有 UI 更新通过 App.Post；App.Quit 最终销毁托盘和 D-Bus 资源，多 App/多 Tray 不共享状态。
- 当前不是 Git 仓库，不初始化、不提交、不使用 sudo。

## Review Focus

- 没有 StatusNotifierWatcher：能力查询为不可用，NewTray 返回可识别错误；由 Task 1 的 `TestTrayUnavailable` 固定。
- D-Bus clicked 与窗口 CloseHide：回调通过 Post 恢复已有窗口，不直接跨线程访问组件；由 Task 2 的 `TestTrayMenuRestoresWindowOnUIThread` 固定。
- 重复 Show/Destroy 和 App.Quit：注册/导出/关闭至多一次，destroy 后回调无效；由 Task 2 的 `TestTrayDestroyIdempotentAndLateEvent` 固定。
- 菜单状态变更和 watcher 重启：revision 更新并可重新注册，不能保留陈旧菜单项；由 Task 2 的 `TestTrayMenuRevisionAndWatcherRestart` 固定。

---

### Task 1: 能力模型与 Linux StatusNotifierItem 核心

**Files:**
- Create: `ui/capability.go`, `ui/tray.go`, `internal/desktop/tray.go`, `internal/desktop/tray_linux.go`, `internal/desktop/tray_unsupported.go`
- Test: `ui/tray_test.go`, `internal/desktop/tray_linux_test.go`

**Interfaces:**
- Produces: `type Capabilities struct { MenuBar, Tray bool }`、`App.Capabilities() Capabilities`。
- `NewTray(icon image.Image, tooltip string) (*Tray, error)` 创建应用无关配置；`App.ShowTray(*Tray) error` 同步完成实际注册并返回结果，`Tray.Hide() error`/`Tray.Destroy() error` 明确清理，`Tray.SetIcon(image.Image) error`、`Tray.SetTooltip(string) error`。
- 内部 `desktop.TrayService` 提供 `Capabilities`、`Show/Update/Hide/Close`，通过构造函数注入 fake D-Bus 供无显示测试。稳定错误可用 `errors.Is(err, ui.ErrTrayUnavailable)` 判断。

- [ ] **Step 1: 写失败测试**：`TestCapabilities`、`TestTrayUnavailable`、`TestTrayIconEncoding`、`TestTrayRegistrationAndProperties`，覆盖 nil/空图标、非零 origin 的 image.Bounds、ARGB 字节序和空 tooltip。
- [ ] **Step 2: 运行 `go test ./ui ./internal/desktop -run 'Test(Capabilities|Tray)' -v`，确认失败。**
- [ ] **Step 3: 实现能力查询和带 build constraint 的平台适配。** Linux 通过 watcher 名称/owner 与 RegisterStatusNotifierItem 判断可用性；不支持平台返回明确状态。
- [ ] **Step 4: 实现唯一服务名、对象导出、IconPixmap/ToolTip 属性、NewIcon/NewToolTip/NewAttentionIcon 信号以及幂等生命周期。** 不使用固定对象名，不覆盖其他进程托盘。
- [ ] **Step 5: 实现 App.ShowTray 的同步注册及清理。** D-Bus 服务自身并发安全且不直接访问 UI 对象，因此 Run 前/运行中均可直接注册；App 用独立互斥锁跟踪托盘所有权，回调只能 Post。发生部分注册失败时回滚，App.Quit 清理托盘且不因同步等待自身 UI 队列而死锁。
- [ ] **Step 6: 运行 `go test ./ui ./internal/desktop ./internal/app`，确认全部通过。**

### Task 2: D-Bus 托盘菜单、窗口恢复与综合验收

**Files:**
- Create: `internal/desktop/dbusmenu_linux.go`, `internal/desktop/dbusmenu_linux_test.go`, `cmd/wind-tray/main.go`, `cmd/wind-tray/main_test.go`
- Modify: `ui/tray.go`, `ui/menu.go`, `ui/app.go`, `internal/desktop/tray_linux.go`

**Interfaces:**
- Tray 提供 `SetMenu(menu *MenuModel) error`；顶层 MenuModel 的 title 不显示，items 映射为 dbusmenu 根节点的 children。
- 使用菜单计划已实现的 `MenuModel.Snapshot()` 和 `MenuItemSnapshot.Activate()`：在 UI 线程取得不可变展示快照并转换为 desktop 自有类型；D-Bus 线程不得直接读取 MenuModel。菜单模型增加包内变更监听，Tray 安装/替换/销毁时正确订阅和退订；SetMenu/SetChecked/SetEnabled 在 UI 线程触发重新快照，增加 layout revision 并发出更新信号。
- 每个菜单项使用稳定递增 ID，支持 enabled、checked、submenu、separator、OnClick；native clicked 只通过 App.Post 调用对应 snapshot 的 Activate，由统一模型复核 enabled/ancestry/membership 并完成 checked-before-callback。
- dbusmenu 实现首版所需的 GetLayout、GetGroupProperties、Event、AboutToShow 及对应返回/信号；clicked 回调不直接访问 Window 或组件。

- [ ] **Step 1: 写失败测试**：`TestDBusMenuLayoutAndEvents`、`TestTrayMenuRevisionAndWatcherRestart`、`TestTrayMenuRestoresWindowOnUIThread`、`TestTrayDestroyIdempotentAndLateEvent`。
- [ ] **Step 2: 运行 `go test ./ui ./internal/desktop -run 'Test(DBusMenu|Tray)' -v`，确认失败。**
- [ ] **Step 3: 实现 dbusmenu 布局、属性、事件与回调桥接。** disabled 项忽略 clicked；普通 checkable 项复用统一菜单切换规则；submenu/separator 不触发动作。
- [ ] **Step 4: 实现 SetMenu 动态更新、watcher 重启重注册和信号退订。** 所有 late event 在关闭后无副作用，不在 D-Bus goroutine 调用 Window.Show。
- [ ] **Step 5: 添加 CloseHide + LastWindowKeepRunning + 托盘恢复窗口的真实演示与确定性测试，明确 CSS 不影响系统托盘菜单。**
- [ ] **Step 6: 运行 `go test ./...`、指定核心包 race、`go vet ./...` 和修改文件的 `gofmt` 检查。** 在真实 Deepin/Fcitx 桌面运行菜单、托盘、恢复、多窗口 smoke；若 watcher/桌面不支持，保留错误证据且不宣称托盘验收通过。
