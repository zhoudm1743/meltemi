# Meltemi

Meltemi 是一个使用 Go 编写的原生桌面 UI 库，目标是提供简洁、链式、符合 Go 使用习惯的组件 API，同时避免引入浏览器运行时。

- 不使用 Fyne 作为底层依赖
- 不使用 WebView、HTML、JavaScript 或浏览器布局引擎
- 使用 GLFW + OpenGL 创建窗口并自绘组件
- 使用 CSS 风格的子集描述组件外观
- 使用 HarfBuzz + FreeType 处理复杂文本
- 通过 D-Bus 接入 Linux 桌面能力

项目当前完成了阶段 0–2 的主要功能：**Linux 基础能力、窗口与组件、菜单与托盘、文件对话框、高 DPI、复杂文本和渲染缓存**。Windows、macOS 和其他 Linux 显示协议仍需后续实现与验证。

## 当前能力

- 链式 `App` / `Window` / `Component` API
- `Column`、`Row`、`Stack`、`Scroll`、`Spacer`
- `Label`、`Button`、`Input`、`Password`、`Checkbox`、`Image`、`TextArea`
- 多窗口、无边框窗口、窗口图标和生命周期配置
- 自绘菜单栏、子菜单、快捷键和 Overlay/Popup
- StatusNotifierItem 托盘与 dbusmenu 菜单
- XDG Desktop Portal 文件打开、保存对话框
- 单行和多行编辑、选择、撤销/重做、词级导航、剪贴板
- Fcitx D-Bus 输入法预编辑和候选位置同步
- CSS 类型、ID、class、状态选择器，以及颜色、边框、圆角、字体、尺寸和透明度
- HarfBuzz 整形、双向文本、组合字符和系统字体回退
- 物理像素文本光栅化与每窗口有界文本纹理缓存

Meltemi 仍处在活跃开发阶段，公共 API 可能继续调整。

## 环境与依赖

### 必需

- Go 1.26 或兼容版本
- Linux / X11
- cgo、C 编译器和 `pkg-config`
- OpenGL 3.3 兼容驱动
- HarfBuzz、FreeType、fontconfig
- 可用的 D-Bus session bus

在 Debian / Deepin 等系统上，可按实际包管理器安装依赖，例如：

```bash
sudo apt install \
  build-essential pkg-config xorg-dev libgl1-mesa-dev \
  libharfbuzz-dev libfreetype-dev libfontconfig-dev libdbus-1-dev
```

不同发行版的包名可能不同。当前代码不要求安装系统 GLFW 开发包。

### 可选桌面能力

- 中文输入：Fcitx 5，目前主要验证 rime 引擎
- 文件对话框：运行中的 `org.freedesktop.portal.Desktop`
- 托盘：可用的 StatusNotifierWatcher，具体表现取决于桌面环境

不支持的能力会通过能力查询或错误返回，不会静默假装成功。

## 获取代码

```bash
git clone https://github.com/zhoudm1743/meltemi.git
cd meltemi
go mod download
go test ./...
```

在其他 Go module 中引用：

```bash
go get github.com/zhoudm1743/meltemi/ui@latest
```

公共组件包为：

```go
import "github.com/zhoudm1743/meltemi/ui"
```

## 快速示例

下面的代码创建一个账号、密码登录窗口：

```go
package main

import (
    "log"

    "github.com/zhoudm1743/meltemi/ui"
)

func main() {
    app := ui.New()
    account := ""
    password := ""

    loginWindow := app.Window("登录").
        Size(360, 240).
        Content(
            ui.Column(
                ui.Input(&account).
                    Class("field").
                    Placeholder("账号"),
                ui.Password(&password).
                    Class("field").
                    Placeholder("密码"),
                ui.Button("登录").
                    ID("login").
                    OnClick(func() {
                        log.Printf("login account=%q", account)
                    }),
            ).Gap(12).Padding(20),
        )

    loginWindow.Show()
    if err := app.Run(); err != nil {
        log.Fatal(err)
    }
}
```

窗口必须在主事件循环中运行，`App.Run` 会锁定 UI 所使用的操作系统线程。

需要 Fcitx 输入时，在 `Run` 前启用：

```go
if err := app.EnableIME(); err != nil {
    log.Fatal(err)
}
```

`EnableIME` 会设置 `MELTEMI_IME=1` 和 `XMODIFIERS=@im=none`。旧的 `WIND_*` 环境变量仍保留读取兼容。

## 外部样式

```go
if err := app.LoadStyles("app.css"); err != nil {
    log.Fatal(err)
}
```

示例 CSS：

```css
.field {
    padding: 8px;
    border: 1px solid #cccccc;
    border-radius: 4px;
}

#login {
    background: #3478f6;
    color: white;
    border-radius: 6px;
}

#login:hover {
    background: #2968d8;
}

#login:disabled {
    opacity: 0.5;
}
```

这里实现的是桌面 UI 所需的 CSS 子集，不是完整浏览器 CSS。代码显式设置的布局参数优先于样式表。

## 菜单、托盘与文件对话框

窗口菜单使用统一的 `Menu` / `MenuItem` 模型，Linux 窗口菜单由 Meltemi 自绘。托盘使用 StatusNotifierItem 和 dbusmenu，由桌面环境绘制。

```go
menu := ui.Menu("文件",
    ui.MenuItem("退出").OnClick(func() {
        app.Quit()
    }),
)

tray, err := ui.NewTray(icon, "Meltemi")
if err != nil {
    log.Fatal(err)
}
if err := tray.SetMenu(menu); err != nil {
    log.Fatal(err)
}
if err := app.ShowTray(tray); err != nil {
    log.Fatal(err)
}

app.SetLastWindowAction(ui.LastWindowKeepRunning)
```

打开文件对话框是异步操作，回调在 UI 线程执行：

```go
dialog, err := app.OpenFile(
    ui.OpenFileOptions{Title: "选择文件", Multiple: true},
    func(paths []string, err error) {
        if err != nil {
            log.Printf("open file: %v", err)
            return
        }
        log.Printf("selected: %v", paths)
    },
)
if err != nil {
    log.Fatal(err)
}
_ = dialog // 可通过 dialog.Close() 主动取消
```

当前 X11 后端尚不能向 portal 提供原生父窗口标识，因此不能承诺 portal 对话框与父窗口的模态关系。

## 演示程序

在仓库根目录运行：

| 命令 | 内容 |
| --- | --- |
| `go run ./cmd/meltemi-login` | 链式登录窗口 |
| `go run ./cmd/meltemi-ui-basic` | 多窗口基础组件 |
| `go run ./cmd/meltemi-layout` | 布局、图片、Checkbox 和透明度 |
| `go run ./cmd/meltemi-notes` | 多行编辑与撤销/重做 |
| `go run ./cmd/meltemi-text` | 中文、连字、组合符、阿拉伯文和混合方向 |
| `go run ./cmd/meltemi-menu` | 多窗口自绘菜单与子菜单 |
| `go run ./cmd/meltemi-tray` | 托盘菜单与恢复窗口 |
| `go run ./cmd/meltemi-dialog` | 原生打开/保存文件对话框 |
| `go run ./cmd/meltemi-frameless` | 无边框窗口与自绘标题栏 |
| `go run ./cmd/meltemi-ime` | Fcitx 输入法和组合输入生命周期 |

部分演示支持 `--font /path/to/font.otf` 指定主字体。未提供中文字体时，也会尝试通过 fontconfig 查找系统回退字体。

## 测试与验证

```bash
go test ./... -count=1
go vet ./...

go test -race ./ui ./internal/desktop ./internal/app ./internal/xim ./internal/driver/glfw -count=1

go test -race -gcflags=all=-d=checkptr=0 \
  ./internal/render/gl ./internal/text -count=1
```

GL 包的 race 命令使用 `checkptr=0` 规避当前 go-gl 版本的指针检查兼容问题，竞态检测本身仍然启用。

真实桌面 smoke 会创建窗口，部分脚本还会操作输入法或桌面服务：

```bash
go run ./cmd/meltemi-layout --smoke
go run ./cmd/meltemi-menu --smoke
go run ./cmd/meltemi-tray --smoke
go run ./cmd/meltemi-dialog --smoke

MELTEMI_GLFW_DESKTOP_TESTS=1 go run ./cmd/meltemi-text --smoke
MELTEMI_GLFW_DESKTOP_TESTS=1 go run ./cmd/meltemi-ime --smoke
```

`meltemi-ime --smoke` 当前要求 **Fcitx rime** 处于活动状态，不会擅自切换输入法。`MELTEMI_GLFW_DESKTOP_TESTS=1` 也可用于启用其他桌面交互测试；旧的 `WIND_GLFW_DESKTOP_TESTS` 仍被接受。

## 平台状态

| 平台/环境 | 状态 |
| --- | --- |
| Deepin / DDE / X11 | 已进行自动化、协议 smoke 和部分视觉验证 |
| 其他 Linux 发行版 | 未验证 |
| GNOME / KDE | 未验证 |
| Wayland | 未验证 |
| Fcitx 5 rime | 已验证输入与资源生命周期 |
| IBus / 其他输入法 | 未验证 |
| Windows / macOS | 尚未完成后端与平台服务 |

详细证据、验证命令和限制见 [doc/兼容性.md](doc/兼容性.md)，架构与阶段规划见 [doc/总体设计.md](doc/总体设计.md)。

## 已知限制

- 当前双向文本采用简化 strong-run 模型，不是完整 UAX#9 实现。
- Grapheme 边界采用文档化子集，不是完整 UAX#29 实现。
- 彩色 emoji 尚未实现，部分字符使用灰度字体或 notdef。
- 不是完整 CSS、浏览器布局或富文本编辑器。
- 托盘图标的显示位置和点击行为依赖桌面宿主。
- 多显示器、分数缩放的实际视觉效果仍需验证。
- 暂不公开任意自定义 Measure/Arrange/Paint 或 Canvas API。

## 参与开发

欢迎提交 Issue 和 Pull Request。提交代码前请运行：

```bash
gofmt -w <修改的 Go 文件>
go test ./... -count=1
go vet ./...
```

涉及窗口、输入法或桌面服务时，请同时运行对应的 `MELTEMI_GLFW_DESKTOP_TESTS=1 ... --smoke` 验证，并在说明中区分自动化结果、协议结果和视觉结果。

## 许可证

当前仓库尚未添加 `LICENSE` 文件，许可证尚未选择。公开可见不等于授予复制、修改或再分发许可；在此之前请保留所有权利。计划复用本项目时，请先与维护者确认许可证。
